package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/routing"
	"github.com/neverknowerdev/paylessforai/internal/usage"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

type translatedStreamResult struct {
	stats     usage.Stats
	committed bool
	err       error
}

func pumpTranslatedStream(writer http.ResponseWriter, clientFormat, providerFormat wire.Format, response *http.Response) translatedStreamResult {
	result := translatedStreamResult{}
	stream, err := wire.NewStreamWriter(clientFormat, writer)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		result.err = err
		return result
	}
	_, result.err = wire.StreamResponse(providerFormat, response, func(event wire.Event) error {
		observeWireEvent(&result.stats, event)
		return stream.Write(event)
	})
	if result.err == nil {
		result.err = stream.End()
	}
	result.committed = stream.Committed()
	return result
}

func observeWireEvent(stats *usage.Stats, event wire.Event) {
	if event.Usage != nil {
		mergeWireUsage(stats, *event.Usage)
	}
	if event.Response != nil {
		mergeWireUsage(stats, event.Response.Usage)
	}
}

func (p *Proxy) learnTranslatedFormat(ctx context.Context, route matcher.Route, format wire.Format) {
	// A valid response remains useful even if format persistence fails.
	if p.Repositories != nil {
		_ = persistLearnedFormat(ctx, p.Repositories, route, format)
	}
	if p.Catalog != nil {
		p.Catalog.LearnFormat(route.ID, format)
	}
}

func (p *Proxy) completeTranslatedStream(ctx context.Context, writer http.ResponseWriter, requestID string, events wire.EventStream, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price, clientFormat wire.Format, upstreamErr error) (err error) {
	stream, err := wire.NewStreamWriter(clientFormat, writer)
	if err != nil {
		return err
	}
	var stats usage.Stats
	// The entire JSON response has already been consumed, including its usage.
	for _, event := range events.Events {
		observeWireEvent(&stats, event)
	}
	defer func() {
		err = p.finalizeStream(ctx, requestID, stats, expectedCost, officialExpectedCost, price, officialPrice, err)
	}()
	for _, event := range events.Events {
		if event.Response != nil && !events.Stream {
			err = stream.WriteResponse(*event.Response)
		} else {
			err = stream.Write(event)
		}
		if err != nil {
			return err
		}
	}
	if upstreamErr != nil {
		return upstreamErr
	}
	return stream.End()
}

func streamOutcome(err error) (state, code string) {
	if err == nil {
		return "succeeded", ""
	}
	if errors.Is(err, context.Canceled) {
		return "partial", "client_disconnected"
	}
	return "partial", "stream_error"
}

// finalizeStream records usage and delivery state on every committed exit,
// including write failures and cancellation. Callers use the returned partial
// error to prohibit retries once the downstream response has started.
func (p *Proxy) finalizeStream(ctx context.Context, requestID string, stats usage.Stats, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price, streamErr error) error {
	finalCtx, cancel := streamFinalizeContext(ctx)
	defer cancel()
	persistUsage(finalCtx, p.Repositories, requestID, stats, expectedCost, officialExpectedCost, price, officialPrice)
	state, code := streamOutcome(streamErr)
	message := ""
	if streamErr != nil {
		message = sanitize(streamErr.Error())
	}
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Complete(finalCtx, requestID, state, code, message)
	}
	if streamErr != nil {
		return &partialStreamError{err: streamErr}
	}
	return nil
}

func streamFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
}

func (p *Proxy) stream(ctx context.Context, writer http.ResponseWriter, requestID string, response *http.Response, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price) (streamErr error) {
	defer response.Body.Close()
	var stats usage.Stats
	defer func() {
		streamErr = p.finalizeStream(ctx, requestID, stats, expectedCost, officialExpectedCost, price, officialPrice, streamErr)
	}()
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	flusher, _ := writer.(http.Flusher)
	reader := bufio.NewReaderSize(response.Body, 64<<10)
	terminal := false
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			observedTerminal, providerErr := observeSSE(line, &stats)
			terminal = terminal || observedTerminal
			if _, writeErr := writer.Write(line); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			if providerErr != nil {
				return providerErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !terminal {
					return errors.New("stream ended before terminal event")
				}
				return nil
			}
			return err
		}
	}
}

func observeSSE(line []byte, stats *usage.Stats) (bool, error) {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return false, nil
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return data == "[DONE]", nil
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(data), &envelope) != nil {
		return false, nil
	}
	observed := usage.FromEnvelope(envelope)
	if observed.InputTokens != 0 {
		stats.InputTokens = observed.InputTokens
	}
	if observed.OutputTokens != 0 {
		stats.OutputTokens = observed.OutputTokens
	}
	if observed.TotalTokens != 0 {
		stats.TotalTokens = observed.TotalTokens
	}
	if observed.CachedReadTokens != 0 {
		stats.CachedReadTokens = observed.CachedReadTokens
	}
	if observed.CacheWriteTokens != 0 {
		stats.CacheWriteTokens = observed.CacheWriteTokens
	}
	if observed.ReasoningTokens != 0 {
		stats.ReasoningTokens = observed.ReasoningTokens
	}
	if observed.ActualCostPicoUSD != nil {
		stats.ActualCostPicoUSD = observed.ActualCostPicoUSD
	}
	stats.Raw = observed.Raw
	typ, _ := envelope["type"].(string)
	if typ == "response.failed" || typ == "response.incomplete" || typ == "error" || envelope["error"] != nil {
		return false, errors.New("provider stream error: " + data)
	}
	return typ == "message_stop" || typ == "response.completed", nil
}

func (p *Proxy) recordTranslatedStreamAttempt(ctx context.Context, requestID string, attempt int, route matcher.Route, entry routing.Entry, clientFormat, providerFormat wire.Format, err error) {
	finalCtx, cancel := streamFinalizeContext(ctx)
	defer cancel()
	state, code := streamOutcome(err)
	p.recordTranslatedAttempt(finalCtx, requestID, attempt, route, entry, clientFormat, providerFormat, state, code, err)
}
