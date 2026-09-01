package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the breaker's timeouts be driven deterministically instead of
// with sleeps, so these tests stay fast and never flake on a loaded CI runner.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var errBoom = errors.New("boom")

func failing(context.Context) error { return errBoom }
func passing(context.Context) error { return nil }

func TestBreakerStaysClosedBelowMinimumRequests(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 10,
		FailureRatio:    0.5,
		Now:             clock.Now,
	})

	// Every call fails, but the sample is too small to be meaningful.
	for i := 0; i < 9; i++ {
		if err := b.Do(context.Background(), failing); !errors.Is(err, errBoom) {
			t.Fatalf("call %d: got %v, want errBoom", i, err)
		}
	}

	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %v, want closed: %d failures is below the minimum sample", got, 9)
	}
}

func TestBreakerOpensOnFailureRatio(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 10,
		FailureRatio:    0.5,
		Now:             clock.Now,
	})

	for i := 0; i < 5; i++ {
		_ = b.Do(context.Background(), passing)
	}
	for i := 0; i < 5; i++ {
		_ = b.Do(context.Background(), failing)
	}

	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %v, want open at 5/10 failures", got)
	}

	// Once open, calls are rejected without touching the dependency.
	called := false
	err := b.Do(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("got %v, want ErrBreakerOpen", err)
	}
	if called {
		t.Fatal("dependency was called while the breaker was open")
	}
}

func TestBreakerRecoversThroughHalfOpen(t *testing.T) {
	clock := newClock()
	var transitions []string
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests:   4,
		FailureRatio:      0.5,
		OpenTimeout:       10 * time.Second,
		HalfOpenSuccesses: 2,
		HalfOpenMaxCalls:  2,
		Now:               clock.Now,
		OnStateChange: func(from, to State) {
			transitions = append(transitions, from.String()+"->"+to.String())
		},
	})

	for i := 0; i < 4; i++ {
		_ = b.Do(context.Background(), failing)
	}
	if b.State() != StateOpen {
		t.Fatal("breaker did not open")
	}

	// Still open before the timeout elapses.
	clock.Advance(9 * time.Second)
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %v, want still open before OpenTimeout", got)
	}

	clock.Advance(2 * time.Second)
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state = %v, want half_open after OpenTimeout", got)
	}

	// Two consecutive trial successes close it.
	for i := 0; i < 2; i++ {
		if err := b.Do(context.Background(), passing); err != nil {
			t.Fatalf("trial %d: %v", i, err)
		}
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %v, want closed after successful trials", got)
	}

	want := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", transitions, want)
		}
	}
}

func TestBreakerReopensOnFailedTrial(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 4,
		FailureRatio:    0.5,
		OpenTimeout:     time.Second,
		Now:             clock.Now,
	})

	for i := 0; i < 4; i++ {
		_ = b.Do(context.Background(), failing)
	}
	clock.Advance(2 * time.Second)
	if b.State() != StateHalfOpen {
		t.Fatal("breaker did not move to half_open")
	}

	// A single failed trial means the dependency is still sick.
	_ = b.Do(context.Background(), failing)
	if got := b.State(); got != StateOpen {
		t.Fatalf("state = %v, want open after a failed trial", got)
	}
}

func TestBreakerHalfOpenLimitsConcurrentTrials(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests:  4,
		FailureRatio:     0.5,
		OpenTimeout:      time.Second,
		HalfOpenMaxCalls: 1,
		Now:              clock.Now,
	})

	for i := 0; i < 4; i++ {
		_ = b.Do(context.Background(), failing)
	}
	clock.Advance(2 * time.Second)

	// Hold one trial open, then confirm a second is rejected rather than
	// piling more load onto a dependency that is still recovering.
	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- b.Do(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	if err := b.Do(context.Background(), passing); !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("second trial: got %v, want ErrBreakerOpen", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first trial: %v", err)
	}
}

func TestBreakerIgnoresCallerCancellation(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 2,
		FailureRatio:    0.5,
		Now:             clock.Now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for i := 0; i < 10; i++ {
		_ = b.Do(ctx, func(ctx context.Context) error { return ctx.Err() })
	}

	// The caller walking away is not evidence the dependency is unhealthy.
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %v, want closed: caller cancellations must not trip the breaker", got)
	}
}

func TestBreakerWindowExpiresOldFailures(t *testing.T) {
	clock := newClock()
	b := NewBreaker("dep", BreakerConfig{
		MinimumRequests: 4,
		FailureRatio:    0.5,
		Window:          10 * time.Second,
		Now:             clock.Now,
	})

	// Three failures, then a long quiet period, then more failures. Spread
	// across separate windows they should never add up to a trip.
	for i := 0; i < 3; i++ {
		_ = b.Do(context.Background(), failing)
	}
	clock.Advance(11 * time.Second)
	for i := 0; i < 3; i++ {
		_ = b.Do(context.Background(), failing)
	}

	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %v, want closed: failures in an expired window must not accumulate", got)
	}
}

func TestBreakerIsRaceFree(t *testing.T) {
	b := NewBreaker("dep", BreakerConfig{MinimumRequests: 5, FailureRatio: 0.5})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if (i+j)%3 == 0 {
					_ = b.Do(context.Background(), failing)
				} else {
					_ = b.Do(context.Background(), passing)
				}
				_ = b.State()
			}
		}(i)
	}
	wg.Wait()
}
