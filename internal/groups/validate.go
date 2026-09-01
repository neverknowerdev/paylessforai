package groups

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SelectionLowestExpectedCost = "lowest_expected_cost"

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func NormalizeSlug(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func ValidateDefinition(def Definition, all map[string]Definition) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	slug := NormalizeSlug(def.Slug)
	if def.Name == "" {
		issues = append(issues, issue("name", "invalid_group_name", "name is required", "error"))
	}
	if slug == "" || !slugPattern.MatchString(slug) || strings.HasSuffix(slug, ":free") {
		issues = append(issues, issue("slug", "invalid_group_slug", "slug must match lowercase [a-z0-9][a-z0-9._-]{0,127} and cannot use the :free suffix", "error"))
	}
	if len(def.Description) > 2000 || len(def.Name) > 200 {
		issues = append(issues, issue("name", "group_text_too_long", "name or description is too long", "error"))
	}
	if other, ok := all[slug]; ok && other.ID != "" && other.ID != def.ID {
		issues = append(issues, issue("slug", "group_slug_conflict", "another group already uses this slug", "error"))
	}
	if len(def.Stages) == 0 {
		issues = append(issues, issue("stages", "invalid_group_stage", "at least one stage is required", "error"))
	}
	positions := make(map[int]bool, len(def.Stages))
	for i, stage := range def.Stages {
		path := fmt.Sprintf("stages[%d]", i)
		if positions[stage.Position] {
			issues = append(issues, issue(path+".position", "invalid_group_stage", "stage positions must be unique", "error"))
		}
		positions[stage.Position] = true
		if stage.Selection == "" {
			stage.Selection = SelectionLowestExpectedCost
		}
		if stage.Selection != SelectionLowestExpectedCost {
			issues = append(issues, issue(path+".selection", "invalid_group_selection", "only lowest_expected_cost is supported", "error"))
		}
		if len(stage.Sources) == 0 {
			issues = append(issues, issue(path+".sources", "invalid_group_stage", "at least one source is required", "error"))
		}
		groupSources := 0
		for j, source := range stage.Sources {
			sourcePath := fmt.Sprintf("%s.sources[%d]", path, j)
			if source.ProviderName != "" && strings.TrimSpace(source.ProviderName) == "" {
				issues = append(issues, issue(sourcePath+".provider_name", "invalid_provider", "provider name cannot be empty", "error"))
			}
			if source.Retries != nil && (*source.Retries < 0 || *source.Retries > 5) {
				issues = append(issues, issue(sourcePath+".retries", "invalid_retry_count", "model retries must be between 0 and 5", "error"))
			}
			if source.MaximumOfficialPricePercent != nil && (*source.MaximumOfficialPricePercent < 0 || *source.MaximumOfficialPricePercent > 100) {
				issues = append(issues, issue(sourcePath+".maximum_official_price_percent", "invalid_price_percent", "auction price percentage must be between 0 and 100", "error"))
			}
			for k, provider := range source.ProviderNames {
				if strings.TrimSpace(provider) == "" {
					issues = append(issues, issue(fmt.Sprintf("%s.provider_names[%d]", sourcePath, k), "invalid_provider", "provider name cannot be empty", "error"))
				}
			}
			if len(source.ProviderNames) > 0 && source.Kind != SourceModel {
				issues = append(issues, issue(sourcePath+".provider_names", "invalid_provider", "provider names can only be set for model sources", "error"))
			}
			if source.Kind == SourceModel && strings.TrimSpace(source.ModelID) != "" && source.GroupID == "" {
				continue
			}
			if source.Kind == SourceGroup && strings.TrimSpace(source.GroupID) != "" && source.ModelID == "" {
				groupSources++
				if !containsGroup(all, source.GroupID) {
					issues = append(issues, issue(sourcePath, "group_not_found", "nested group does not exist", "error"))
				}
				continue
			}
			issues = append(issues, issue(sourcePath, "invalid_group_source", "source must contain exactly one model_id or group_id", "error"))
		}
		if stage.TryRetries != nil && (*stage.TryRetries < 0 || *stage.TryRetries > 5) {
			issues = append(issues, issue(path+".try_retries", "invalid_retry_count", "try retries must be between 0 and 5", "error"))
		}
		if groupSources > 0 && (groupSources != 1 || len(stage.Sources) != 1) {
			issues = append(issues, issue(path+".sources", "invalid_group_source", "a nested stage must contain exactly one group source", "error"))
		}
		for j, provider := range stage.ProviderNames {
			if strings.TrimSpace(provider) == "" {
				issues = append(issues, issue(fmt.Sprintf("%s.provider_names[%d]", path, j), "invalid_provider", "provider name cannot be empty", "error"))
			}
		}
		seenBilling := map[BillingClass]bool{}
		for j, billing := range stage.BillingClasses {
			if billing != BillingFree && billing != BillingSubscription && billing != BillingMetered {
				issues = append(issues, issue(fmt.Sprintf("%s.billing_classes[%d]", path, j), "invalid_billing_class", "billing class must be free, subscription, or metered", "error"))
			}
			if seenBilling[billing] {
				issues = append(issues, issue(path+".billing_classes", "invalid_billing_class", "billing classes must be unique", "error"))
			}
			seenBilling[billing] = true
		}
		if len(stage.BillingClasses) == 0 {
			issues = append(issues, issue(path+".billing_classes", "invalid_billing_class", "at least one billing class is required", "error"))
		}
		for field, value := range map[string]*int64{"maximum_input_pico_usd_per_token": stage.MaximumInputPicoUSDPerToken, "maximum_output_pico_usd_per_token": stage.MaximumOutputPicoUSDPerToken, "maximum_expected_cost_pico_usd": stage.MaximumExpectedCostPicoUSD} {
			if value != nil && *value < 0 {
				issues = append(issues, issue(path+"."+field, "invalid_cost_limit", "cost limits must be non-negative", "error"))
			}
		}
		if stage.SameRouteRetries != nil && (*stage.SameRouteRetries < 0 || *stage.SameRouteRetries > 5) {
			issues = append(issues, issue(path+".same_route_retries", "invalid_retry_count", "same-route retries must be between 0 and 5", "error"))
		}
	}
	if len(positions) > 0 {
		ordered := make([]int, 0, len(positions))
		for position := range positions {
			ordered = append(ordered, position)
		}
		sort.Ints(ordered)
		for index, position := range ordered {
			if position != index {
				issues = append(issues, issue("stages", "invalid_group_stage", "stage positions must be contiguous starting at zero", "error"))
				break
			}
		}
	}
	return issues
}

func containsGroup(all map[string]Definition, id string) bool {
	if _, ok := all[id]; ok {
		return true
	}
	for _, value := range all {
		if value.ID == id {
			return true
		}
	}
	return false
}

func issue(path, code, message, level string) ValidationIssue {
	return ValidationIssue{Path: path, Code: code, Message: message, Level: level}
}

func normalizeStage(stage Stage) Stage {
	stage.ProviderNames = uniqueLower(stage.ProviderNames)
	if len(stage.BillingClasses) == 0 {
		stage.BillingClasses = append([]BillingClass(nil), AllBillingClasses...)
	}
	seen := map[BillingClass]bool{}
	billing := make([]BillingClass, 0, len(stage.BillingClasses))
	for _, value := range stage.BillingClasses {
		if !seen[value] {
			seen[value] = true
			billing = append(billing, value)
		}
	}
	stage.BillingClasses = billing
	if stage.Selection == "" {
		stage.Selection = SelectionLowestExpectedCost
	}
	for i := range stage.Sources {
		stage.Sources[i].ModelID = strings.TrimSpace(stage.Sources[i].ModelID)
		stage.Sources[i].GroupID = strings.TrimSpace(stage.Sources[i].GroupID)
		stage.Sources[i].ProviderName = strings.ToLower(strings.TrimSpace(stage.Sources[i].ProviderName))
		stage.Sources[i].ProviderNames = uniqueLower(stage.Sources[i].ProviderNames)
	}
	return stage
}

func uniqueLower(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
