package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// Permanent wraps an error to tell Retry to stop immediately.
//
// Retrying a 400 or a validation failure cannot succeed and only multiplies
// load, so callers classify the failure and the retry loop obeys.
type Permanent struct{ Err error }

func (p *Permanent) Error() string { return p.Err.Error() }
func (p *Permanent) Unwrap() error { return p.Err }

// Permanently marks err as not worth retrying.
func Permanently(err error) error { return &Permanent{Err: err} }

// RetryConfig bounds a retry loop.
type RetryConfig struct {
	// MaxAttempts counts the initial call, so 3 means one call plus two retries.
	MaxAttempts int
	// BaseDelay is the first backoff interval; it doubles each attempt.
	BaseDelay time.Duration
	// MaxDelay caps the exponential growth.
	MaxDelay time.Duration
	// JitterFraction in [0,1] randomises each delay by that fraction.
	//
	// Without jitter every caller that failed at the same moment retries at the
	// same moment, and the recovering dependency is hit by a synchronised
	// thundering herd instead of a smooth ramp.
	JitterFraction float64
	// Rand is injectable for deterministic tests; defaults to a shared source.
	Rand func() float64
	// Sleep is injectable for tests; defaults to a context-aware timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnRetry, when set, is called before each retry with the 1-based attempt
	// that just failed and the delay about to be waited.
	OnRetry func(attempt int, delay time.Duration, err error)
}

func (c *RetryConfig) withDefaults() {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 50 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 2 * time.Second
	}
	if c.JitterFraction < 0 || c.JitterFraction > 1 {
		c.JitterFraction = 0.3
	}
	if c.Rand == nil {
		c.Rand = rand.Float64
	}
	if c.Sleep == nil {
		c.Sleep = sleepCtx
	}
}

// Retry calls fn until it succeeds, exhausts attempts, hits a Permanent error,
// or the context ends.
//
// The context deadline outranks the attempt budget: if the caller's deadline
// would elapse during a backoff, Retry returns rather than sleeping past it.
func Retry(ctx context.Context, cfg RetryConfig, fn func(ctx context.Context, attempt int) error) error {
	cfg.withDefaults()

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		err := fn(ctx, attempt)
		if err == nil {
			return nil
		}
		lastErr = err

		var permanent *Permanent
		if errors.As(err, &permanent) {
			return permanent.Err
		}
		if attempt == cfg.MaxAttempts {
			break
		}
		// The caller's cancellation is theirs, not the dependency's fault.
		if ctx.Err() != nil {
			break
		}

		delay := cfg.delayFor(attempt)
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
			break
		}
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, delay, err)
		}
		if serr := cfg.Sleep(ctx, delay); serr != nil {
			return lastErr
		}
	}
	return lastErr
}

// delayFor returns the jittered exponential backoff after the given attempt.
func (c RetryConfig) delayFor(attempt int) time.Duration {
	backoff := float64(c.BaseDelay) * math.Pow(2, float64(attempt-1))
	if backoff > float64(c.MaxDelay) {
		backoff = float64(c.MaxDelay)
	}
	if c.JitterFraction > 0 {
		// Symmetric jitter around the nominal backoff, so the mean delay still
		// grows exponentially while individual callers spread out.
		spread := backoff * c.JitterFraction
		backoff = backoff - spread + (2 * spread * c.Rand())
	}
	if backoff < 0 {
		backoff = 0
	}
	return time.Duration(backoff)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
