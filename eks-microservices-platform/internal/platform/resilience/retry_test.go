package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

// noSleep records the delays a retry loop would have waited without actually
// waiting, keeping the tests deterministic and instant.
type recorder struct{ delays []time.Duration }

func (r *recorder) sleep(ctx context.Context, d time.Duration) error {
	r.delays = append(r.delays, d)
	return ctx.Err()
}

func TestRetrySucceedsWithoutRetrying(t *testing.T) {
	rec := &recorder{}
	calls := 0
	err := Retry(context.Background(), RetryConfig{Sleep: rec.sleep}, func(context.Context, int) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(rec.delays) != 0 {
		t.Fatalf("slept %v on a successful first attempt", rec.delays)
	}
}

func TestRetryStopsAtMaxAttempts(t *testing.T) {
	rec := &recorder{}
	calls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 3, Sleep: rec.sleep}, func(context.Context, int) error {
		calls++
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (MaxAttempts counts the initial call)", calls)
	}
	if len(rec.delays) != 2 {
		t.Fatalf("delays = %v, want 2 backoffs between 3 attempts", rec.delays)
	}
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	calls := 0
	sentinel := errors.New("bad request")
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 5}, func(context.Context, int) error {
		calls++
		return Permanently(sentinel)
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the unwrapped sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: a permanent error must not be retried", calls)
	}
}

func TestRetryBackoffGrowsExponentiallyAndIsCapped(t *testing.T) {
	// Pin the jitter to its midpoint so the nominal schedule is observable.
	cfg := RetryConfig{
		MaxAttempts:    6,
		BaseDelay:      100 * time.Millisecond,
		MaxDelay:       500 * time.Millisecond,
		JitterFraction: 0,
		Rand:           func() float64 { return 0.5 },
	}
	cfg.withDefaults()

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond, // capped
		500 * time.Millisecond,
	}
	for i, w := range want {
		if got := cfg.delayFor(i + 1); got != w {
			t.Fatalf("delayFor(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRetryJitterStaysWithinBounds(t *testing.T) {
	cfg := RetryConfig{
		BaseDelay:      100 * time.Millisecond,
		MaxDelay:       time.Second,
		JitterFraction: 0.5,
	}
	cfg.withDefaults()

	// With a 50% jitter fraction the first delay must land in [50ms, 150ms].
	low, high := 50*time.Millisecond, 150*time.Millisecond
	for i := 0; i < 500; i++ {
		got := cfg.delayFor(1)
		if got < low || got > high {
			t.Fatalf("delayFor(1) = %v, want within [%v, %v]", got, low, high)
		}
	}
}

func TestRetryJitterActuallyVaries(t *testing.T) {
	cfg := RetryConfig{BaseDelay: 100 * time.Millisecond, JitterFraction: 0.5}
	cfg.withDefaults()

	seen := make(map[time.Duration]struct{})
	for i := 0; i < 100; i++ {
		seen[cfg.delayFor(2)] = struct{}{}
	}
	// The whole point of jitter is that simultaneous callers desynchronise.
	if len(seen) < 10 {
		t.Fatalf("only %d distinct delays across 100 draws; jitter is not spreading retries", len(seen))
	}
}

func TestRetryHonoursContextDeadlineOverAttemptBudget(t *testing.T) {
	// A 10-attempt budget with 1s backoffs must not outlive a 50ms deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	calls := 0
	start := time.Now()
	err := Retry(ctx, RetryConfig{
		MaxAttempts:    10,
		BaseDelay:      time.Second,
		JitterFraction: 0,
	}, func(context.Context, int) error {
		calls++
		return errBoom
	})

	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the last dependency error", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("retry ran for %v; it slept past the caller's deadline", elapsed)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: the next backoff exceeded the deadline", calls)
	}
}

func TestRetryReportsAttemptNumber(t *testing.T) {
	rec := &recorder{}
	var seen []int
	_ = Retry(context.Background(), RetryConfig{MaxAttempts: 3, Sleep: rec.sleep}, func(_ context.Context, attempt int) error {
		seen = append(seen, attempt)
		return errBoom
	})
	want := []int{1, 2, 3}
	for i := range want {
		if i >= len(seen) || seen[i] != want[i] {
			t.Fatalf("attempts = %v, want %v", seen, want)
		}
	}
}

func TestRetryComposesWithBreaker(t *testing.T) {
	// The two protections have to work together: retries must not be able to
	// hammer a dependency the breaker has already given up on.
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 3,
		FailureRatio:    0.5,
		Now:             clock.Now,
	})

	rec := &recorder{}
	dependencyCalls := 0
	err := Retry(context.Background(), RetryConfig{MaxAttempts: 10, Sleep: rec.sleep}, func(ctx context.Context, _ int) error {
		return b.Do(ctx, func(context.Context) error {
			dependencyCalls++
			return errBoom
		})
	})

	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("err = %v, want ErrBreakerOpen once the breaker trips", err)
	}
	if dependencyCalls != 3 {
		t.Fatalf("dependency was called %d times; the breaker should have cut it off at 3", dependencyCalls)
	}
}
