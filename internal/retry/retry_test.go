package retry

import (
	"testing"
	"time"
)

func baseInput(class ErrorClass) Input {
	now := time.Unix(100, 0)
	return Input{Policy: Policy{MaximumAttempts: 3, BaseDelay: time.Second, MaximumDelay: 4 * time.Second, MaximumRetryAfter: 10 * time.Second}, AttemptNumber: 1, StartedAt: now, Now: now, Error: ClassifiedError{Class: class}, Delivery: NothingSent, SameRouteAvailable: true, FallbacksRemaining: 1}
}

func TestRetryDecisionTable(t *testing.T) {
	tests := []struct {
		name   string
		input  Input
		action Action
		health HealthEffect
		delay  time.Duration
	}{
		{"invalid request", baseInput(ErrorInvalidRequest), TerminalError, NoHealthChange, 0},
		{"first transient retries same", baseInput(ErrorTimeout), RetrySameRoute, BackoffRoute, time.Second},
		{"free route fails over immediately", func() Input { in := baseInput(ErrorServer); in.SameRouteAvailable = false; return in }(), FailOver, BackoffRoute, time.Second},
		{"second transient fails over", func() Input { in := baseInput(ErrorServer); in.AttemptNumber = 2; return in }(), FailOver, BackoffRoute, 2 * time.Second},
		{"auth fails over and backs off credential", baseInput(ErrorAuthentication), FailOver, BackoffCredential, time.Second},
		{"payment fails over and backs off credential", baseInput(ErrorPayment), FailOver, BackoffCredential, time.Second},
		{"model not found fails over and refreshes", baseInput(ErrorModelNotFound), FailOver, MarkRouteStale, time.Second},
		{"malformed fails over", baseInput(ErrorMalformedResponse), FailOver, BackoffRoute, time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := New().Decide(test.input)
			if got.Action != test.action || got.HealthEffect != test.health || got.Delay != test.delay {
				t.Fatalf("got %#v, want action=%s health=%s delay=%s", got, test.action, test.health, test.delay)
			}
		})
	}
}

func TestRetryNeverFailsOverAfterClientVisibleBytes(t *testing.T) {
	for _, delivery := range []DeliveryState{HeadersSent, BodyStarted, StreamCompleted} {
		input := baseInput(ErrorServer)
		input.Delivery = delivery
		decision := New().Decide(input)
		if decision.Action != PartialStream {
			t.Fatalf("delivery %s: got %s", delivery, decision.Action)
		}
	}
}

func TestRetryCancellationAndBudget(t *testing.T) {
	input := baseInput(ErrorCancelled)
	if got := New().Decide(input).Action; got != ClientCancelled {
		t.Fatalf("got %s", got)
	}
	input = baseInput(ErrorTimeout)
	input.AttemptNumber = 3
	if got := New().Decide(input).Action; got != TerminalError {
		t.Fatalf("got %s", got)
	}
	input = baseInput(ErrorTimeout)
	input.SameRouteAvailable = false
	input.FallbacksRemaining = 0
	if got := New().Decide(input).Action; got != TerminalError {
		t.Fatalf("got %s", got)
	}
}

func TestRetryAfterIsBoundedAndDeadlineIsRespected(t *testing.T) {
	input := baseInput(ErrorRateLimit)
	retryAfter := 3 * time.Second
	input.Error.RetryAfter = &retryAfter
	if got := New().Decide(input); got.Delay != retryAfter {
		t.Fatalf("got %#v", got)
	}
	tooLong := 20 * time.Second
	input.Error.RetryAfter = &tooLong
	if got := New().Decide(input); got.Delay != time.Second {
		t.Fatalf("expected fallback backoff, got %#v", got)
	}
	input.Error.RetryAfter = nil
	input.Deadline = input.Now.Add(500 * time.Millisecond)
	if got := New().Decide(input).Action; got != TerminalError {
		t.Fatalf("expected deadline terminal, got %s", got)
	}
}

func TestRetryBackoffCaps(t *testing.T) {
	policy := Policy{MaximumAttempts: 10, BaseDelay: time.Second, MaximumDelay: 4 * time.Second, MaximumRetryAfter: 10 * time.Second}
	for attempt, expected := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 4 * time.Second} {
		input := baseInput(ErrorTransport)
		input.Policy = policy
		input.AttemptNumber = attempt
		if got := New().Decide(input).Delay; got != expected {
			t.Fatalf("attempt %d: got %s want %s", attempt, got, expected)
		}
	}
}
