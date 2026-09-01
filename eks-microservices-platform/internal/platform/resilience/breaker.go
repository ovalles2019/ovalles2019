// Package resilience provides the call-path protections a service needs before
// it can be trusted to fan out to other services: a circuit breaker so a sick
// dependency cannot exhaust this service's goroutines and connections, and a
// bounded retry policy that will not amplify an outage it is sitting on top of.
package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBreakerOpen is returned instead of calling the dependency while the
// breaker is open.
var ErrBreakerOpen = errors.New("circuit breaker is open")

// State is the breaker's position in the closed -> open -> half-open cycle.
type State int

const (
	// StateClosed passes every call through and counts outcomes.
	StateClosed State = iota
	// StateOpen rejects every call immediately, giving the dependency room to
	// recover instead of holding it under load while it is failing.
	StateOpen
	// StateHalfOpen admits a small number of trial calls to decide whether the
	// dependency has recovered.
	StateHalfOpen
)

// String renders the state for logs and the state-change metric.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerConfig tunes when a breaker trips and how it recovers.
type BreakerConfig struct {
	// MinimumRequests is how many calls must land in a window before the
	// failure ratio is allowed to trip the breaker. Without it a single failed
	// request at 100% failure rate would open the circuit.
	MinimumRequests int
	// FailureRatio in [0,1] that trips the breaker once MinimumRequests is met.
	FailureRatio float64
	// OpenTimeout is how long the breaker stays open before admitting trials.
	OpenTimeout time.Duration
	// HalfOpenMaxCalls bounds concurrent trial calls in the half-open state so
	// a recovering dependency is not immediately re-flooded.
	HalfOpenMaxCalls int
	// HalfOpenSuccesses is how many consecutive trial successes close the
	// breaker again.
	HalfOpenSuccesses int
	// Window is the rolling period over which outcomes are counted.
	Window time.Duration
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// OnStateChange, when set, is called on every transition. It must not block.
	OnStateChange func(from, to State)
}

func (c *BreakerConfig) withDefaults() {
	if c.MinimumRequests <= 0 {
		c.MinimumRequests = 20
	}
	if c.FailureRatio <= 0 || c.FailureRatio > 1 {
		c.FailureRatio = 0.5
	}
	if c.OpenTimeout <= 0 {
		c.OpenTimeout = 15 * time.Second
	}
	if c.HalfOpenMaxCalls <= 0 {
		c.HalfOpenMaxCalls = 3
	}
	if c.HalfOpenSuccesses <= 0 {
		c.HalfOpenSuccesses = 2
	}
	if c.Window <= 0 {
		c.Window = 30 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Breaker is a concurrency-safe circuit breaker around one dependency.
type Breaker struct {
	name string
	cfg  BreakerConfig

	mu             sync.Mutex
	state          State
	successes      int
	failures       int
	windowStart    time.Time
	openedAt       time.Time
	halfOpenActive int
	halfOpenOK     int
}

// NewBreaker returns a closed breaker named for the dependency it guards.
func NewBreaker(name string, cfg BreakerConfig) *Breaker {
	cfg.withDefaults()
	return &Breaker{
		name:        name,
		cfg:         cfg,
		state:       StateClosed,
		windowStart: cfg.Now(),
	}
}

// Name returns the dependency name this breaker guards.
func (b *Breaker) Name() string { return b.name }

// State returns the current state, applying any pending open -> half-open
// transition first.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked()
	return b.state
}

// Do runs fn unless the breaker is open, and records the outcome.
//
// A context cancellation from the caller is returned without being counted as a
// dependency failure: the caller giving up says nothing about the dependency's
// health, and counting it would let client timeouts trip the circuit.
func (b *Breaker) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := b.allow(); err != nil {
		return err
	}

	err := fn(ctx)

	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		b.release()
		return err
	}
	b.record(err == nil)
	return err
}

// allow reserves capacity for one call, or reports why the call is rejected.
func (b *Breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.maybeHalfOpenLocked()

	switch b.state {
	case StateOpen:
		return ErrBreakerOpen
	case StateHalfOpen:
		if b.halfOpenActive >= b.cfg.HalfOpenMaxCalls {
			return ErrBreakerOpen
		}
		b.halfOpenActive++
		return nil
	default:
		b.rollWindowLocked()
		return nil
	}
}

// release returns half-open capacity for a call that produced no verdict.
func (b *Breaker) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateHalfOpen && b.halfOpenActive > 0 {
		b.halfOpenActive--
	}
}

func (b *Breaker) record(ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		if b.halfOpenActive > 0 {
			b.halfOpenActive--
		}
		if !ok {
			// A single failed trial is enough to re-open: the dependency has
			// not recovered and further trials would just add load.
			b.openLocked()
			return
		}
		b.halfOpenOK++
		if b.halfOpenOK >= b.cfg.HalfOpenSuccesses {
			b.transitionLocked(StateClosed)
			b.resetCountsLocked()
		}
	case StateClosed:
		b.rollWindowLocked()
		if ok {
			b.successes++
			return
		}
		b.failures++
		total := b.successes + b.failures
		if total >= b.cfg.MinimumRequests && float64(b.failures)/float64(total) >= b.cfg.FailureRatio {
			b.openLocked()
		}
	}
}

func (b *Breaker) openLocked() {
	b.transitionLocked(StateOpen)
	b.openedAt = b.cfg.Now()
	b.resetCountsLocked()
}

func (b *Breaker) maybeHalfOpenLocked() {
	if b.state == StateOpen && b.cfg.Now().Sub(b.openedAt) >= b.cfg.OpenTimeout {
		b.transitionLocked(StateHalfOpen)
		b.halfOpenActive = 0
		b.halfOpenOK = 0
	}
}

func (b *Breaker) rollWindowLocked() {
	if b.cfg.Now().Sub(b.windowStart) >= b.cfg.Window {
		b.windowStart = b.cfg.Now()
		b.successes = 0
		b.failures = 0
	}
}

func (b *Breaker) resetCountsLocked() {
	b.successes = 0
	b.failures = 0
	b.halfOpenActive = 0
	b.halfOpenOK = 0
	b.windowStart = b.cfg.Now()
}

func (b *Breaker) transitionLocked(to State) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	if b.cfg.OnStateChange != nil {
		b.cfg.OnStateChange(from, to)
	}
}
