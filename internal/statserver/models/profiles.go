package models

import "time"

type Profile struct {
	Key               string  `json:"key"`
	DisplayName       string  `json:"display_name"`
	Description       string  `json:"description"`
	State             string  `json:"state"`
	MissingDataPolicy string  `json:"missing_data_policy"`
	VersionID         int64   `json:"version_id"`
	Version           int     `json:"version"`
	MinimumCoverage   float64 `json:"minimum_coverage"`
}

type ProfileVersion struct {
	ProfileID, ID                       int64
	Key, DisplayName, MissingDataPolicy string
	MinimumCoverage                     float64
	Components                          []ProfileComponent
}

type ProfileComponent struct {
	SignalType, Selector, Direction string
	Weight                          int
	Required                        bool
	MinValue, MaxValue              float64
}

type ScoreComponent struct {
	Selector string  `json:"selector"`
	Value    float64 `json:"value"`
	Weight   int     `json:"weight"`
}

type CapabilityScore struct {
	Key          string    `json:"key"`
	DisplayName  string    `json:"display_name"`
	Version      int       `json:"version"`
	Score        float64   `json:"score"`
	BaseScore    float64   `json:"base_score"`
	Coverage     float64   `json:"coverage"`
	CalculatedAt time.Time `json:"calculated_at"`
}

type CreateProfile struct{ Key, DisplayName, Description string }
type CreateSignal struct{ Key, DisplayName, Description string }
type CreateComponent struct {
	SignalType, Selector, Direction, Rationale string
	Weight                                     int
	Required                                   bool
	MinValue, MaxValue                         float64
}
