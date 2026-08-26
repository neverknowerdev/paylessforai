package routing

import (
	"time"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

type Entry struct {
	StageID          string        `json:"stage_id"`
	StagePath        []string      `json:"stage_path"`
	Route            matcher.Route `json:"route"`
	ExpectedCost     int64         `json:"expected_cost"`
	SameRouteRetries int           `json:"same_route_retries"`
}

type Plan struct {
	RequestedModel string                   `json:"requested_model"`
	GroupID        string                   `json:"group_id,omitempty"`
	GroupRevision  int64                    `json:"group_revision,omitempty"`
	Entries        []Entry                  `json:"entries"`
	Rejections     []matcher.RouteRejection `json:"rejections,omitempty"`
	Error          *matcher.MatchError      `json:"error,omitempty"`
}

func (p Plan) Selected() *Entry {
	if len(p.Entries) == 0 {
		return nil
	}
	return &p.Entries[0]
}
func (p Plan) MaxAttempts() int {
	total := 0
	for _, entry := range p.Entries {
		total += 1 + entry.SameRouteRetries
	}
	return total
}

type Limits struct {
	MaximumRoutes   int
	MaximumAttempts int
}

func DefaultLimits() Limits { return Limits{MaximumRoutes: 128, MaximumAttempts: 32} }

func BuildDirect(request matcher.MatchRequest, routes []matcher.Route, now time.Time) Plan {
	result := Plan{RequestedModel: request.LogicalModel}
	matched := matcher.New().Match(matcher.MatchInput{Request: request, Routes: routes, Now: now})
	for _, ranked := range matched.Ranked {
		retries := 1
		if ranked.Route.Free {
			retries = 0
		}
		result.Entries = append(result.Entries, Entry{Route: ranked.Route, ExpectedCost: ranked.ExpectedCost, SameRouteRetries: retries})
	}
	result.Rejections, result.Error = matched.Rejections, matched.Error
	return result
}

func BuildGroup(request matcher.MatchRequest, definition groups.Definition, definitions map[string]groups.Definition, routes []matcher.Route, now time.Time, limits Limits) Plan {
	if limits.MaximumRoutes <= 0 {
		limits.MaximumRoutes = 128
	}
	if limits.MaximumAttempts <= 0 {
		limits.MaximumAttempts = 32
	}
	result := Plan{RequestedModel: request.LogicalModel, GroupID: definition.ID, GroupRevision: definition.Revision}
	compiled := groups.Compile(definition, definitions, groups.DefaultCompileLimits())
	for _, issue := range compiled.Issues {
		if issue.Level == "error" {
			result.Error = &matcher.MatchError{Code: issue.Code, Message: issue.Message}
			return result
		}
	}
	seen := map[string]bool{}
	for _, stage := range compiled.Stages {
		stageRequest := request
		stageRequest.LogicalModel = ""
		stageRequest.LogicalModels = append([]string(nil), stage.LogicalModelIDs...)
		stageRequest.AllowedProviders = append([]string(nil), stage.ProviderNames...)
		stageRequest.AllowedBillingClasses = make([]matcher.BillingClass, 0, len(stage.BillingClasses))
		for _, value := range stage.BillingClasses {
			stageRequest.AllowedBillingClasses = append(stageRequest.AllowedBillingClasses, matcher.BillingClass(value))
		}
		stageRequest.MaximumInputPicoUSDPerToken = stage.MaximumInputPicoUSDPerToken
		stageRequest.MaximumOutputPicoUSDPerToken = stage.MaximumOutputPicoUSDPerToken
		stageRequest.MaximumCostPicoUSD = stage.MaximumExpectedCostPicoUSD
		matched := matcher.New().Match(matcher.MatchInput{Request: stageRequest, Routes: routes, Now: now})
		result.Rejections = append(result.Rejections, matched.Rejections...)
		for _, ranked := range matched.Ranked {
			if seen[ranked.Route.ID] {
				continue
			}
			seen[ranked.Route.ID] = true
			result.Entries = append(result.Entries, Entry{StageID: stage.Name, StagePath: append([]string(nil), stage.Path...), Route: ranked.Route, ExpectedCost: ranked.ExpectedCost, SameRouteRetries: stage.SameRouteRetries})
			if len(result.Entries) >= limits.MaximumRoutes {
				result.Error = &matcher.MatchError{Code: "group_plan_too_large", Message: "resolved group has too many routes"}
				return result
			}
		}
	}
	if len(result.Entries) == 0 {
		capOnly := len(result.Rejections) > 0
		hasCapRejection := false
		for _, rejection := range result.Rejections {
			if rejection.Code == "over_input_price_limit" || rejection.Code == "over_output_price_limit" || rejection.Code == "over_maximum_cost" {
				hasCapRejection = true
				continue
			}
			// Routes excluded by an explicit stage policy do not make a
			// price-cap failure less actionable. For example, a metered-only
			// stage commonly sees free routes rejected by billing class before
			// the metered candidate is rejected by its price ceiling.
			if rejection.Code != "wrong_billing_class" && rejection.Code != "provider_not_allowed" && rejection.Code != "provider_excluded" {
				capOnly = false
				break
			}
		}
		if capOnly && hasCapRejection {
			result.Error = &matcher.MatchError{Code: "group_price_limit_exceeded", Message: "no compatible route satisfies the group price limit"}
		} else {
			result.Error = &matcher.MatchError{Code: "no_eligible_route", Message: "no healthy compatible route is available"}
		}
	}
	if result.MaxAttempts() > limits.MaximumAttempts {
		result.Error = &matcher.MatchError{Code: "group_plan_too_large", Message: "group attempt budget exceeds the safety limit"}
	}
	return result
}
