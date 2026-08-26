// Package retry contains deterministic retry and failover decisions.
package retry

import "time"

type ErrorClass string

const (
	ErrorInvalidRequest    ErrorClass = "invalid_request"
	ErrorAuthentication    ErrorClass = "authentication"
	ErrorPayment           ErrorClass = "payment_required"
	ErrorModelNotFound     ErrorClass = "model_not_found"
	ErrorRateLimit         ErrorClass = "rate_limit"
	ErrorQuotaExhausted    ErrorClass = "quota_exhausted"
	ErrorTimeout           ErrorClass = "timeout"
	ErrorTransport         ErrorClass = "transport"
	ErrorServer            ErrorClass = "server"
	ErrorMalformedResponse ErrorClass = "malformed_response"
	ErrorCancelled         ErrorClass = "cancelled"
	ErrorUnknown           ErrorClass = "unknown"
)

type DeliveryState string

const (
	NothingSent     DeliveryState = "nothing_sent"
	HeadersSent     DeliveryState = "headers_sent"
	BodyStarted     DeliveryState = "body_started"
	StreamCompleted DeliveryState = "stream_completed"
)

type Action string

const (
	RetrySameRoute  Action = "retry_same_route"
	FailOver        Action = "fail_over"
	TerminalError   Action = "return_terminal_error"
	PartialStream   Action = "terminate_partial_stream"
	ClientCancelled Action = "return_client_cancelled"
)

type HealthEffect string

const (
	NoHealthChange    HealthEffect = "none"
	BackoffRoute      HealthEffect = "backoff_route"
	BackoffCredential HealthEffect = "backoff_credential"
	MarkRouteStale    HealthEffect = "mark_route_stale"
	BlockCredential   HealthEffect = "block_credential"
)

type ClassifiedError struct {
	Class           ErrorClass
	HTTPStatus      int
	RetryAfter      *time.Duration
	Description     string
	NextAvailableAt *time.Time
}

type Policy struct {
	MaximumAttempts   int
	BaseDelay         time.Duration
	MaximumDelay      time.Duration
	MaximumRetryAfter time.Duration
}

func DefaultPolicy() Policy {
	return Policy{MaximumAttempts: 3, BaseDelay: 100 * time.Millisecond, MaximumDelay: 2 * time.Second, MaximumRetryAfter: 5 * time.Second}
}

type Input struct {
	Policy             Policy
	AttemptNumber      int // 1-based completed attempt number.
	StartedAt          time.Time
	Now                time.Time
	Deadline           time.Time
	Error              ClassifiedError
	Delivery           DeliveryState
	SameRouteAvailable bool
	FallbacksRemaining int
}

type Decision struct {
	Action       Action
	Delay        time.Duration
	HealthEffect HealthEffect
	Reason       string
}

type Engine struct{}

func New() Engine { return Engine{} }

func (Engine) Decide(input Input) Decision {
	if input.Error.Class == ErrorCancelled {
		return Decision{Action: ClientCancelled, HealthEffect: NoHealthChange, Reason: "client cancelled the request"}
	}
	if input.Delivery == HeadersSent || input.Delivery == BodyStarted || input.Delivery == StreamCompleted {
		return Decision{Action: PartialStream, HealthEffect: NoHealthChange, Reason: "client-visible response bytes prevent safe failover"}
	}
	if input.Error.Class == ErrorInvalidRequest {
		return Decision{Action: TerminalError, HealthEffect: NoHealthChange, Reason: "request validation errors are terminal"}
	}
	if input.Error.Class == ErrorQuotaExhausted {
		if input.FallbacksRemaining > 0 {
			return Decision{Action: FailOver, HealthEffect: BlockCredential, Reason: "provider quota is exhausted until its reset"}
		}
		return Decision{Action: TerminalError, HealthEffect: BlockCredential, Reason: "provider quota is exhausted"}
	}
	policy := normalizePolicy(input.Policy)
	if input.AttemptNumber < 1 || input.AttemptNumber >= policy.MaximumAttempts {
		return Decision{Action: TerminalError, HealthEffect: NoHealthChange, Reason: "retry attempt budget exhausted"}
	}
	if input.Deadline.IsZero() == false && !input.Now.Before(input.Deadline) {
		return Decision{Action: TerminalError, HealthEffect: NoHealthChange, Reason: "request deadline exhausted"}
	}

	health := NoHealthChange
	if input.Error.Class == ErrorAuthentication || input.Error.Class == ErrorPayment {
		health = BackoffCredential
	} else if input.Error.Class == ErrorModelNotFound {
		health = MarkRouteStale
	} else if retryable(input.Error.Class) {
		health = BackoffRoute
	} else {
		return Decision{Action: TerminalError, HealthEffect: NoHealthChange, Reason: "error class is terminal"}
	}

	action := RetrySameRoute
	if !input.SameRouteAvailable || input.AttemptNumber > 1 || input.Error.Class == ErrorAuthentication || input.Error.Class == ErrorPayment || input.Error.Class == ErrorModelNotFound || input.Error.Class == ErrorMalformedResponse {
		if input.FallbacksRemaining > 0 {
			action = FailOver
		} else if !input.SameRouteAvailable {
			return Decision{Action: TerminalError, HealthEffect: health, Reason: "no retry route remains"}
		}
	}
	delay := backoff(policy, input.AttemptNumber)
	if input.Error.RetryAfter != nil && *input.Error.RetryAfter >= 0 && *input.Error.RetryAfter <= policy.MaximumRetryAfter {
		delay = *input.Error.RetryAfter
	}
	if !input.Deadline.IsZero() {
		remaining := input.Deadline.Sub(input.Now)
		if remaining <= 0 || delay >= remaining {
			return Decision{Action: TerminalError, HealthEffect: health, Reason: "retry delay would exceed request deadline"}
		}
	}
	return Decision{Action: action, Delay: delay, HealthEffect: health, Reason: "bounded retryable provider failure"}
}

func retryable(class ErrorClass) bool {
	switch class {
	case ErrorRateLimit, ErrorTimeout, ErrorTransport, ErrorServer, ErrorAuthentication, ErrorPayment, ErrorModelNotFound, ErrorMalformedResponse:
		return true
	default:
		return false
	}
}

func normalizePolicy(policy Policy) Policy {
	if policy.MaximumAttempts < 1 {
		policy.MaximumAttempts = 1
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = 100 * time.Millisecond
	}
	if policy.MaximumDelay < policy.BaseDelay {
		policy.MaximumDelay = policy.BaseDelay
	}
	if policy.MaximumRetryAfter <= 0 {
		policy.MaximumRetryAfter = policy.MaximumDelay
	}
	return policy
}

func backoff(policy Policy, attempt int) time.Duration {
	delay := policy.BaseDelay
	for i := 1; i < attempt && delay < policy.MaximumDelay; i++ {
		if delay > policy.MaximumDelay/2 {
			return policy.MaximumDelay
		}
		delay *= 2
	}
	if delay > policy.MaximumDelay {
		return policy.MaximumDelay
	}
	return delay
}
