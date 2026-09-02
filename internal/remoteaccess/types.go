package remoteaccess

import (
	"context"
	"net"
	"net/http"
	"time"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModePrivate  Mode = "private"
	ModeFunnel   Mode = "funnel"
)

type Phase string

const (
	PhaseDisabled     Phase = "disabled"
	PhaseStarting     Phase = "starting"
	PhaseAuthRequired Phase = "auth_required"
	PhaseConnecting   Phase = "connecting"
	PhasePublishing   Phase = "publishing"
	PhaseOnline       Phase = "online"
	PhaseStopping     Phase = "stopping"
	PhaseError        Phase = "error"
)

const (
	configKey     = "remote_access.config.v1"
	lastResultKey = "remote_access.last_result.v1"
	defaultHost   = "paylessforai"
	maxHostname   = 63
)

type Config struct {
	Mode     Mode   `json:"mode"`
	Hostname string `json:"hostname"`
}

type Action struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

type ErrorInfo struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

type Status struct {
	DesiredMode   Mode       `json:"desired_mode"`
	EffectiveMode Mode       `json:"effective_mode"`
	Phase         Phase      `json:"phase"`
	Hostname      string     `json:"hostname"`
	DNSName       string     `json:"dns_name,omitempty"`
	DashboardURL  string     `json:"dashboard_url,omitempty"`
	BaseURL       string     `json:"base_url,omitempty"`
	Action        *Action    `json:"action,omitempty"`
	LastError     *ErrorInfo `json:"last_error,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type SettingsStore interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
}

type Node interface {
	Start() error
	Status(context.Context) (*NodeStatus, error)
	WhoIs(context.Context, string) (string, error)
	ListenTLS(string, string) (net.Listener, error)
	ListenFunnel(string, string) (net.Listener, error)
	Close() error
}

type NodeStatus struct {
	Running bool
	AuthURL string
	DNSName string
	OwnerID string
}

type NodeFactory func(hostname, dir string) Node
type Authorizer func(http.Handler) http.Handler
type PrivateHandlerFactory func(Authorizer) http.Handler

type Controller interface {
	Status(context.Context) Status
	Configure(context.Context, Config) error
	Retry(context.Context) error
	Stop(context.Context) error
	ForgetIdentity(context.Context) error
}
