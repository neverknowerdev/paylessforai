package groups

import "testing"

func retry(value int) *int     { return &value }
func price(value int64) *int64 { return &value }

func TestValidateRejectsAmbiguousSourcesAndInvalidSlug(t *testing.T) {
	definition := Definition{Name: "Bad", Slug: "Bad Group", Stages: []Stage{{Position: 0, Sources: []Source{{Kind: SourceModel, ModelID: "model-a"}, {Kind: SourceGroup, GroupID: "child"}}, BillingClasses: []BillingClass{BillingMetered}}}}
	issues := ValidateDefinition(definition, map[string]Definition{"child": {ID: "child", Slug: "child"}})
	seen := map[string]bool{}
	for _, item := range issues {
		seen[item.Code] = true
	}
	if !seen["invalid_group_slug"] || !seen["invalid_group_source"] {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestCompileNestedGroupsIntersectsPolicyAndPreservesOrder(t *testing.T) {
	child := Definition{ID: "child", Slug: "child", Enabled: true, Stages: []Stage{{Position: 0, Name: "models", Sources: []Source{{Kind: SourceModel, ModelID: "model-a"}, {Kind: SourceModel, ModelID: "model-b"}}, BillingClasses: []BillingClass{BillingMetered, BillingSubscription}, MaximumInputPicoUSDPerToken: price(10), SameRouteRetries: retry(2)}}}
	root := Definition{ID: "root", Slug: "root", Enabled: true, Stages: []Stage{{Position: 0, Name: "child fallback", Sources: []Source{{Kind: SourceGroup, GroupID: "child"}}, ProviderNames: []string{"OpenRouter"}, BillingClasses: []BillingClass{BillingMetered}, MaximumInputPicoUSDPerToken: price(7), SameRouteRetries: retry(1)}}}
	result := Compile(root, map[string]Definition{"root": root, "child": child}, DefaultCompileLimits())
	if len(result.Issues) != 0 || len(result.Stages) != 1 {
		t.Fatalf("unexpected compile: %#v", result)
	}
	stage := result.Stages[0]
	if len(stage.LogicalModelIDs) != 2 || stage.ProviderNames[0] != "openrouter" || len(stage.BillingClasses) != 1 || stage.BillingClasses[0] != BillingMetered || *stage.MaximumInputPicoUSDPerToken != 7 || stage.SameRouteRetries != 1 {
		t.Fatalf("policy was not composed: %#v", stage)
	}
	if len(stage.Path) != 4 || stage.Path[0] != "root" || stage.Path[2] != "child" {
		t.Fatalf("unexpected path: %#v", stage.Path)
	}
}

func TestCompileDetectsIndirectCycle(t *testing.T) {
	a := Definition{ID: "a", Slug: "a", Enabled: true, Stages: []Stage{{Position: 0, Sources: []Source{{Kind: SourceGroup, GroupID: "b"}}, BillingClasses: []BillingClass{BillingMetered}}}}
	b := Definition{ID: "b", Slug: "b", Enabled: true, Stages: []Stage{{Position: 0, Sources: []Source{{Kind: SourceGroup, GroupID: "a"}}, BillingClasses: []BillingClass{BillingMetered}}}}
	result := Compile(a, map[string]Definition{"a": a, "b": b}, DefaultCompileLimits())
	if len(result.Issues) == 0 || result.Issues[0].Code != "group_cycle" {
		t.Fatalf("expected cycle, got %#v", result.Issues)
	}
}

func TestCompileDisabledChildIsWarningAndEmpty(t *testing.T) {
	child := Definition{ID: "child", Slug: "child", Enabled: false}
	root := Definition{ID: "root", Slug: "root", Enabled: true, Stages: []Stage{{Position: 0, Sources: []Source{{Kind: SourceGroup, GroupID: "child"}}, BillingClasses: []BillingClass{BillingMetered}}}}
	result := Compile(root, map[string]Definition{"root": root, "child": child}, DefaultCompileLimits())
	if len(result.Stages) != 0 || len(result.Issues) != 1 || result.Issues[0].Level != "warning" {
		t.Fatalf("unexpected disabled child result: %#v", result)
	}
}
