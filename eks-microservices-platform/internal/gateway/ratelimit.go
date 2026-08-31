package gateway

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

// RateLimiter is a per-client token bucket.
//
// It is deliberately in-process rather than backed by Redis. Each replica
// therefore enforces limit/N of the global rate, which is the correct tradeoff
// here: a shared-store limiter puts a network round trip and a new failure mode
// on the hot path of every request. See docs/adr/0008-in-process-rate-limiting.md
// for when that stops being true.
type RateLimiter struct {
	rate     float64 // tokens added per second
	burst    float64 // bucket capacity
	ttl      time.Duration
	now      func() time.Time
	stopOnce sync.Once
	stop     chan struct{}

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
	seen   time.Time
}

// RateLimiterConfig configures a RateLimiter.
type RateLimiterConfig struct {
	// RequestsPerSecond is the sustained rate allowed per client.
	RequestsPerSecond float64
	// Burst is the maximum instantaneous burst.
	Burst int
	// IdleTTL is how long an unused bucket is retained.
	IdleTTL time.Duration
	// Now is injectable for tests.
	Now func() time.Time
}

// NewRateLimiter returns a limiter and starts its eviction loop.
//
// Eviction matters: a limiter keyed by client identity that never evicts is an
// unbounded map an attacker fills by rotating keys until the pod is OOMKilled.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	if cfg.RequestsPerSecond <= 0 {
		cfg.RequestsPerSecond = 50
	}
	if cfg.Burst <= 0 {
		cfg.Burst = int(cfg.RequestsPerSecond)
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	rl := &RateLimiter{
		rate:    cfg.RequestsPerSecond,
		burst:   float64(cfg.Burst),
		ttl:     cfg.IdleTTL,
		now:     cfg.Now,
		stop:    make(chan struct{}),
		buckets: make(map[string]*bucket),
	}
	go rl.evictLoop()
	return rl
}

// Close stops the eviction goroutine.
func (rl *RateLimiter) Close() {
	rl.stopOnce.Do(func() { close(rl.stop) })
}

func (rl *RateLimiter) evictLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-t.C:
			rl.evict()
		}
	}
}

func (rl *RateLimiter) evict() {
	cutoff := rl.now().Add(-rl.ttl)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, b := range rl.buckets {
		if b.seen.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}
}

// Allow consumes one token for key, reporting whether the request may proceed
// and how long to wait when it may not.
func (rl *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := rl.now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, exists := rl.buckets[key]
	if !exists {
		b = &bucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}

	// Refill for elapsed time, capped at the burst size.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rl.rate
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.last = now
	}
	b.seen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Time until one whole token is available again.
	deficit := 1 - b.tokens
	return false, time.Duration(deficit / rl.rate * float64(time.Second))
}

// Buckets returns the number of tracked clients, for tests and diagnostics.
func (rl *RateLimiter) Buckets() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

// Middleware rejects requests that exceed the caller's budget with a 429 and a
// Retry-After header, so a well-behaved client can back off correctly instead
// of guessing.
func (rl *RateLimiter) Middleware(keyFor func(*http.Request) string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFor(r)
			ok, retryAfter := rl.Allow(key)
			if !ok {
				seconds := int(retryAfter.Seconds())
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				httpx.WriteProblem(w, r, http.StatusTooManyRequests, "rate_limited",
					"Request rate exceeded. Retry after the interval in the Retry-After header.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
