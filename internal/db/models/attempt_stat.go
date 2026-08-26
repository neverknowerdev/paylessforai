package models

type AttemptStat struct {
	Number        int64  `json:"number"`
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model"`
	State         string `json:"state"`
	StartedAt     string `json:"started_at"`
	CompletedAt   string `json:"completed_at,omitempty"`
	DurationMS    *int64 `json:"duration_ms,omitempty"`
	ErrorClass    string `json:"error_class,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	RawError      string `json:"raw_error,omitempty"`
}
