package groups

import (
	"fmt"
	"sort"
	"strings"
)

type CompileLimits struct {
	MaximumDepth  int
	MaximumStages int
}

func DefaultCompileLimits() CompileLimits { return CompileLimits{MaximumDepth: 8, MaximumStages: 24} }

func Compile(root Definition, all map[string]Definition, limits CompileLimits) CompileResult {
	if limits.MaximumDepth <= 0 {
		limits.MaximumDepth = 8
	}
	if limits.MaximumStages <= 0 {
		limits.MaximumStages = 24
	}
	result := CompileResult{}
	stack := map[string]bool{}
	var visit func(Definition, int, []string, inheritedPolicy) bool
	visit = func(def Definition, depth int, prefix []string, inherited inheritedPolicy) bool {
		if depth > limits.MaximumDepth {
			result.Issues = append(result.Issues, issue("stages", "group_depth_exceeded", "nested group depth exceeds the safety limit", "error"))
			return false
		}
		if stack[def.ID] {
			result.Issues = append(result.Issues, issue("stages", "group_cycle", fmt.Sprintf("group cycle includes %s", def.Slug), "error"))
			return false
		}
		stack[def.ID] = true
		stages := append([]Stage(nil), def.Stages...)
		sort.SliceStable(stages, func(i, j int) bool { return stages[i].Position < stages[j].Position })
		for _, raw := range stages {
			stage := normalizeStage(raw)
			tryKey := strings.Join(append(append([]string(nil), prefix...), def.Slug, fmt.Sprintf("%d", raw.Position)), "\x00")
			if len(stage.Sources) == 1 && stage.Sources[0].Kind == SourceGroup {
				child, ok := all[stage.Sources[0].GroupID]
				if !ok {
					result.Issues = append(result.Issues, issue("stages", "group_not_found", "nested group does not exist", "error"))
					continue
				}
				if !child.Enabled {
					result.Issues = append(result.Issues, issue("stages", "disabled_nested_group", "nested group is disabled; this stage will be empty", "warning"))
					continue
				}
				childPolicy := sourcePolicy(composePolicy(inherited, stage), stage.Sources[0])
				childPolicy.tryKey = tryKey
				childPrefix := append(append([]string(nil), prefix...), def.Slug, stage.Name)
				visit(child, depth+1, childPrefix, childPolicy)
				continue
			}
			path := append(append([]string(nil), prefix...), def.Slug, stage.Name)
			sourceSpecific := false
			for _, source := range stage.Sources {
				if source.Kind == SourceModel && (source.ProviderName != "" || len(source.ProviderNames) > 0 || source.Retries != nil || source.MaximumOfficialPricePercent != nil) {
					sourceSpecific = true
				}
			}
			if sourceSpecific {
				for _, source := range stage.Sources {
					if source.Kind != SourceModel || strings.TrimSpace(source.ModelID) == "" {
						continue
					}
					effective := effectiveStage(sourcePolicy(composePolicy(inherited, stage), source), Stage{Name: stage.Name, Sources: []Source{source}, Selection: stage.Selection})
					effective.Path = path
					effective.TryKey = tryKey
					if len(effective.LogicalModelIDs) > 0 {
						result.Stages = append(result.Stages, effective)
					}
				}
			} else {
				effective := effectiveStage(composePolicy(inherited, stage), stage)
				// A model source without an explicit provider is a model-wide
				// block. Subscription plans are intentionally never part of that
				// block; they must be selected as an explicit provider-model
				// source so their rate-limit warning and semantics remain visible.
				if hasModelWideSource(stage.Sources) {
					effective.BillingClasses = withoutSubscription(effective.BillingClasses)
				}
				effective.Path = path
				effective.TryKey = tryKey
				if len(effective.LogicalModelIDs) > 0 {
					result.Stages = append(result.Stages, effective)
				}
			}
			if len(result.Stages) > limits.MaximumStages {
				result.Issues = append(result.Issues, issue("stages", "group_plan_too_large", "resolved group has too many stages", "error"))
				stack[def.ID] = false
				return false
			}
		}
		stack[def.ID] = false
		return true
	}
	if root.Enabled {
		visit(root, 1, nil, inheritedPolicy{})
	} else {
		result.Issues = append(result.Issues, issue("enabled", "group_disabled", "group is disabled", "error"))
	}
	return result
}

type inheritedPolicy struct {
	providers       []string
	billing         []BillingClass
	limits          PriceLimits
	retries         *int
	retryLocked     bool
	officialPercent *int
	tryRetries      *int
	tryRetryLocked  bool
	tryKey          string
}

func composePolicy(parent inheritedPolicy, stage Stage) inheritedPolicy {
	result := parent
	result.providers = intersectStrings(parent.providers, stage.ProviderNames)
	if len(parent.providers) == 0 {
		result.providers = append([]string(nil), stage.ProviderNames...)
	}
	result.billing = intersectBilling(parent.billing, stage.BillingClasses)
	if len(parent.billing) == 0 {
		result.billing = append([]BillingClass(nil), stage.BillingClasses...)
	}
	result.limits.MaximumInputPicoUSDPerToken = minPtr(parent.limits.MaximumInputPicoUSDPerToken, stage.MaximumInputPicoUSDPerToken)
	result.limits.MaximumOutputPicoUSDPerToken = minPtr(parent.limits.MaximumOutputPicoUSDPerToken, stage.MaximumOutputPicoUSDPerToken)
	result.limits.MaximumExpectedCostPicoUSD = minPtr(parent.limits.MaximumExpectedCostPicoUSD, stage.MaximumExpectedCostPicoUSD)
	if stage.SameRouteRetries != nil && !parent.retryLocked {
		value := *stage.SameRouteRetries
		result.retries = &value
		result.retryLocked = true
	}
	if stage.TryRetries != nil && !parent.tryRetryLocked {
		value := *stage.TryRetries
		result.tryRetries = &value
		result.tryRetryLocked = true
	}
	return result
}

func sourcePolicy(parent inheritedPolicy, source Source) inheritedPolicy {
	result := parent
	if source.ProviderName != "" {
		result.providers = intersectStrings(parent.providers, []string{strings.ToLower(strings.TrimSpace(source.ProviderName))})
		if len(parent.providers) == 0 {
			result.providers = []string{strings.ToLower(strings.TrimSpace(source.ProviderName))}
		}
	} else if !source.IncludeNewProviders && len(source.ProviderNames) > 0 {
		result.providers = intersectStrings(parent.providers, source.ProviderNames)
		if len(parent.providers) == 0 {
			result.providers = append([]string(nil), source.ProviderNames...)
		}
	}
	if source.Kind == SourceModel && source.ProviderName == "" {
		result.billing = withoutSubscription(result.billing)
	}
	if source.Retries != nil && !parent.retryLocked {
		value := *source.Retries
		result.retries = &value
		result.retryLocked = true
	}
	if source.MaximumOfficialPricePercent != nil {
		result.officialPercent = minIntPtr(parent.officialPercent, source.MaximumOfficialPricePercent)
	}
	return result
}

func hasModelWideSource(sources []Source) bool {
	for _, source := range sources {
		if source.Kind == SourceModel && strings.TrimSpace(source.ProviderName) == "" {
			return true
		}
	}
	return false
}

func withoutSubscription(values []BillingClass) []BillingClass {
	result := make([]BillingClass, 0, len(values))
	for _, value := range values {
		if value != BillingSubscription {
			result = append(result, value)
		}
	}
	// An empty AllowedBillingClasses slice means "no billing filter" to the
	// matcher. Keep a non-matching sentinel when a model-wide source has no
	// non-subscription billing classes, so subscription routes cannot leak
	// back into the candidate set.
	if len(values) > 0 && len(result) == 0 {
		return []BillingClass{"__no_eligible_billing__"}
	}
	return result
}

func effectiveStage(parent inheritedPolicy, stage Stage) EffectiveStage {
	policy := composePolicy(parent, stage)
	result := EffectiveStage{Name: stage.Name, LogicalModelIDs: []string{}, ProviderNames: policy.providers, BillingClasses: policy.billing, Selection: stage.Selection, MaximumInputPicoUSDPerToken: policy.limits.MaximumInputPicoUSDPerToken, MaximumOutputPicoUSDPerToken: policy.limits.MaximumOutputPicoUSDPerToken, MaximumExpectedCostPicoUSD: policy.limits.MaximumExpectedCostPicoUSD, TryKey: policy.tryKey, MaximumOfficialPricePercent: policy.officialPercent}
	if stage.SameRouteRetries != nil && !parent.retryLocked {
		result.SameRouteRetries = *stage.SameRouteRetries
	} else if policy.retries != nil {
		result.SameRouteRetries = *policy.retries
	}
	if policy.tryRetries != nil {
		result.TryRetries = *policy.tryRetries
	}
	for _, source := range stage.Sources {
		if source.Kind == SourceModel {
			result.LogicalModelIDs = appendUnique(result.LogicalModelIDs, source.ModelID)
		}
	}
	return result
}

func minIntPtr(values ...*int) *int {
	var selected *int
	for _, value := range values {
		if value != nil && (selected == nil || *value < *selected) {
			copy := *value
			selected = &copy
		}
	}
	return selected
}

func minPtr(values ...*int64) *int64 {
	var selected *int64
	for _, value := range values {
		if value != nil && (selected == nil || *value < *selected) {
			copy := *value
			selected = &copy
		}
	}
	return selected
}
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func intersectStrings(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	set := map[string]bool{}
	for _, value := range b {
		set[value] = true
	}
	out := []string{}
	for _, value := range a {
		if set[value] {
			out = appendUnique(out, value)
		}
	}
	return out
}
func intersectBilling(a, b []BillingClass) []BillingClass {
	if len(a) == 0 {
		return append([]BillingClass(nil), b...)
	}
	if len(b) == 0 {
		return append([]BillingClass(nil), a...)
	}
	set := map[BillingClass]bool{}
	for _, value := range b {
		set[value] = true
	}
	out := []BillingClass{}
	for _, value := range a {
		if set[value] {
			out = appendUniqueBilling(out, value)
		}
	}
	return out
}
func appendUniqueBilling(values []BillingClass, value BillingClass) []BillingClass {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
