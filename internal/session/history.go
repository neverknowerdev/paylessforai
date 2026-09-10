package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/wire"
)

const (
	maxHistoryItems = 32
	maxHistoryBytes = 256 << 10
)

type historyItem struct {
	kind  string
	value string
}

func extractHistory(format wire.Format, body []byte) ([]historyItem, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	var raw json.RawMessage
	var ok bool
	if format == wire.FormatResponses {
		raw, ok = payload["input"]
	} else {
		raw, ok = payload["messages"]
	}
	if !ok {
		return nil, false
	}
	var values []historyItem
	var supported bool
	switch format {
	case wire.FormatChatCompletions, wire.FormatAnthropicMessages:
		var messages []any
		if err := json.Unmarshal(raw, &messages); err != nil {
			return nil, false
		}
		values, supported = extractMessages(messages)
	case wire.FormatResponses:
		var input any
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, false
		}
		values, supported = extractResponsesInput(input)
	default:
		return nil, false
	}
	if !supported || len(values) == 0 {
		return nil, supported
	}
	return values, true
}

func extractMessages(messages []any) ([]historyItem, bool) {
	result := make([]historyItem, 0, len(messages))
	for _, raw := range messages {
		if len(result) >= maxHistoryItems {
			break
		}
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		role, _ := message["role"].(string)
		if role != string(wire.RoleUser) {
			continue
		}
		item, keep, supported := extractContent(message["content"])
		if !supported {
			return nil, false
		}
		if keep {
			result = append(result, item)
		}
	}
	return result, historyWithinLimit(result)
}

func extractResponsesInput(input any) ([]historyItem, bool) {
	if text, ok := input.(string); ok {
		item, keep, supported := normalizedItem(historyItem{kind: "text", value: text})
		if !supported {
			return nil, false
		}
		if !keep {
			return nil, true
		}
		return []historyItem{item}, true
	}
	items, ok := input.([]any)
	if !ok {
		return nil, false
	}
	result := make([]historyItem, 0, len(items))
	for _, raw := range items {
		if len(result) >= maxHistoryItems {
			break
		}
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		typ, _ := item["type"].(string)
		switch strings.ToLower(typ) {
		case "message":
			role, _ := item["role"].(string)
			if role != string(wire.RoleUser) {
				continue
			}
			content, keep, supported := extractContent(item["content"])
			if !supported {
				return nil, false
			}
			if keep {
				result = append(result, content)
			}
		case "function_call", "function_call_output", "reasoning", "computer_call", "computer_call_output":
			continue
		default:
			return nil, false
		}
	}
	return result, historyWithinLimit(result)
}

func extractContent(raw any) (historyItem, bool, bool) {
	switch value := raw.(type) {
	case string:
		return normalizedItem(historyItem{kind: "text", value: value})
	case []any:
		parts := make([]string, 0, len(value))
		kind := "text"
		for _, rawPart := range value {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return historyItem{}, false, false
			}
			typ, _ := part["type"].(string)
			switch strings.ToLower(typ) {
			case "text", "input_text":
				text, ok := part["text"].(string)
				if !ok {
					return historyItem{}, false, false
				}
				parts = append(parts, text)
			case "tool_result", "tool_use", "tool_call", "function_call", "function_call_output":
				// Tool activity is deliberately excluded, including tool-result-only
				// user messages.
				continue
			case "image", "input_image", "image_url", "audio", "input_audio":
				digest, supported := inlineMediaDigest(part)
				if !supported {
					return historyItem{}, false, false
				}
				parts = append(parts, digest)
				kind = "mixed"
			default:
				return historyItem{}, false, false
			}
		}
		return normalizedItem(historyItem{kind: kind, value: strings.Join(parts, "\n")})
	default:
		return historyItem{}, false, false
	}
}

func normalizedItem(item historyItem) (historyItem, bool, bool) {
	item.value = strings.TrimSpace(strings.ReplaceAll(item.value, "\r\n", "\n"))
	if item.value == "" {
		return historyItem{}, false, true
	}
	return item, true, true
}

func historyWithinLimit(items []historyItem) bool {
	total := 0
	for _, item := range items {
		total += len([]byte(item.value))
		if total > maxHistoryBytes {
			return false
		}
	}
	return true
}

func inlineMediaDigest(part map[string]any) (string, bool) {
	var value any
	for _, key := range []string{"data", "image_url", "audio_url", "source"} {
		if candidate, exists := part[key]; exists {
			value = candidate
			break
		}
	}
	if nested, ok := value.(map[string]any); ok {
		if typ, _ := nested["type"].(string); strings.EqualFold(typ, "url") {
			return "", false
		}
		if url, ok := nested["url"].(string); ok {
			value = url
		} else {
			value = nested["data"]
		}
	}
	text, ok := value.(string)
	if !ok || text == "" || strings.HasPrefix(strings.ToLower(text), "http://") || strings.HasPrefix(strings.ToLower(text), "https://") {
		return "", false
	}
	digest := sha256.Sum256([]byte(text))
	return fmt.Sprintf("[%s:%x]", part["type"], digest[:]), true
}
