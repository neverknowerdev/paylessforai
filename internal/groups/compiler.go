package groups

import (
	"fmt"
	"sort"
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
				childPolicy := composePolicy(inherited, stage)
				childPrefix := append(append([]string(nil), prefix...), def.Slug, stage.Name)
				visit(child, depth+1, childPrefix, childPolicy)
				continue
			}
			effective := effectiveStage(inherited, stage)
			effective.Path = append(append([]string(nil), prefix...), def.Slug, stage.Name)
			if len(effective.LogicalModelIDs) == 0 {
				continue
			}
			result.Stages = append(result.Stages, effective)
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
	providers   []string
	billing     []BillingClass
	limits      PriceLimits
	retries     *int
	retryLocked bool
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
	return result
}

func effectiveStage(parent inheritedPolicy, stage Stage) EffectiveStage {
	policy := composePolicy(parent, stage)
	result := EffectiveStage{Name: stage.Name, LogicalModelIDs: []string{}, ProviderNames: policy.providers, BillingClasses: policy.billing, Selection: stage.Selection, MaximumInputPicoUSDPerToken: policy.limits.MaximumInputPicoUSDPerToken, MaximumOutputPicoUSDPerToken: policy.limits.MaximumOutputPicoUSDPerToken, MaximumExpectedCostPicoUSD: policy.limits.MaximumExpectedCostPicoUSD}
	if stage.SameRouteRetries != nil && !parent.retryLocked {
		result.SameRouteRetries = *stage.SameRouteRetries
	} else if policy.retries != nil {
		result.SameRouteRetries = *policy.retries
	}
	for _, source := range stage.Sources {
		if source.Kind == SourceModel {
			result.LogicalModelIDs = appendUnique(result.LogicalModelIDs, source.ModelID)
		}
	}
	return result
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
