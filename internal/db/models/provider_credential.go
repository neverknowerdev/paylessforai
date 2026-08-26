package models

type ProviderCredential struct {
	ID                     string  `json:"id"`
	Provider               string  `json:"provider"`
	Label                  string  `json:"label"`
	BaseURL                string  `json:"base_url,omitempty"`
	Ciphertext             []byte  `json:"-"`
	Nonce                  []byte  `json:"-"`
	Enabled                bool    `json:"enabled"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
	LastCheckedAt          *string `json:"last_checked_at,omitempty"`
	LastError              *string `json:"last_error,omitempty"`
	ManualModelsJSON       string  `json:"-"`
	AccessMode             string  `json:"access_mode"`
	SubscriptionFeePicoUSD *int64  `json:"subscription_fee_pico_usd,omitempty"`
	SubscriptionCycleStart *string `json:"subscription_cycle_start,omitempty"`
	SubscriptionCycleEnd   *string `json:"subscription_cycle_end,omitempty"`
	SubscriptionStatus     string  `json:"subscription_status"`
	NextAvailableAt        *string `json:"next_available_at,omitempty"`
	StatusReason           *string `json:"status_reason,omitempty"`
}
