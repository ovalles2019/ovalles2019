package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

func protected(store *APIKeyStore) http.Handler {
	return httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(ClientFrom(r.Context())))
		}),
		httpx.RequestID(),
		Authenticate(store),
	)
}

func TestAuthenticateAcceptsValidKey(t *testing.T) {
	store := NewAPIKeyStore(map[string]string{"fleet-ops": "s3cret-key"})
	h := protected(store)

	for _, header := range []struct{ name, key, value string }{
		{"bearer", "Authorization", "Bearer s3cret-key"},
		{"api key header", "X-API-Key", "s3cret-key"},
	} {
		t.Run(header.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(header.key, header.value)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Body.String(); got != "fleet-ops" {
				t.Fatalf("client = %q, want fleet-ops", got)
			}
		})
	}
}

func TestAuthenticateRejectsBadKeys(t *testing.T) {
	store := NewAPIKeyStore(map[string]string{"fleet-ops": "s3cret-key"})
	h := protected(store)

	cases := []struct{ name, header string }{
		{"no credential", ""},
		{"wrong key", "Bearer wrong-key"},
		{"prefix of a valid key", "Bearer s3cret"},
		{"valid key with suffix", "Bearer s3cret-keyx"},
		{"malformed scheme", "Basic s3cret-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("WWW-Authenticate is missing, so a client cannot tell how to authenticate")
			}
		})
	}
}

func TestAuthenticateIsOpenWhenNoKeysConfigured(t *testing.T) {
	// Local development must work with no secrets present.
	h := protected(NewAPIKeyStore(nil))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != AnonymousClient {
		t.Fatalf("client = %q, want %q", got, AnonymousClient)
	}
}

func TestAPIKeysAreNotHeldInPlaintext(t *testing.T) {
	// A heap dump or an accidental log of the store must not yield usable keys.
	store := NewAPIKeyStore(map[string]string{"fleet-ops": "s3cret-key"})
	for stored := range store.byDigest {
		if stored == "s3cret-key" {
			t.Fatal("the raw API key is retained in memory; only its digest should be")
		}
		if len(stored) != 64 {
			t.Fatalf("stored value %q is not a sha256 hex digest", stored)
		}
	}
}

func TestParseKeySpec(t *testing.T) {
	store := ParseKeySpec("fleet-ops:key-a, analytics:key-b ,,malformed, :missing-id, missing-key:")

	if store.Len() != 2 {
		t.Fatalf("Len = %d, want 2: malformed entries must be dropped, not partially accepted", store.Len())
	}
	for key, wantClient := range map[string]string{"key-a": "fleet-ops", "key-b": "analytics"} {
		got, ok := store.Lookup(key)
		if !ok || got != wantClient {
			t.Fatalf("Lookup(%q) = %q,%v; want %q,true", key, got, ok, wantClient)
		}
	}
}

func TestRateLimiterAllowsBurstThenThrottles(t *testing.T) {
	clock := time.Unix(0, 0)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: 10,
		Burst:             3,
		Now:               func() time.Time { return clock },
	})
	defer rl.Close()

	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("client"); !ok {
			t.Fatalf("request %d rejected inside the burst allowance", i)
		}
	}

	ok, retryAfter := rl.Allow("client")
	if ok {
		t.Fatal("the fourth request was allowed past a burst of 3")
	}
	if retryAfter <= 0 {
		t.Fatal("no Retry-After hint was produced, so a client cannot back off correctly")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	clock := time.Unix(0, 0)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: 10,
		Burst:             1,
		Now:               func() time.Time { return clock },
	})
	defer rl.Close()

	if ok, _ := rl.Allow("client"); !ok {
		t.Fatal("first request rejected")
	}
	if ok, _ := rl.Allow("client"); ok {
		t.Fatal("second immediate request allowed")
	}

	// At 10/s one token is restored after 100ms.
	clock = clock.Add(100 * time.Millisecond)
	if ok, _ := rl.Allow("client"); !ok {
		t.Fatal("bucket did not refill after the token interval elapsed")
	}
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	clock := time.Unix(0, 0)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		Now:               func() time.Time { return clock },
	})
	defer rl.Close()

	if ok, _ := rl.Allow("noisy"); !ok {
		t.Fatal("first client rejected")
	}
	if ok, _ := rl.Allow("noisy"); ok {
		t.Fatal("noisy client exceeded its own budget")
	}
	// One client exhausting its bucket must not affect anyone else.
	if ok, _ := rl.Allow("quiet"); !ok {
		t.Fatal("a second client was throttled by the first client's usage")
	}
}

func TestRateLimiterEvictsIdleBuckets(t *testing.T) {
	// Without eviction, rotating the client key is an unbounded memory leak.
	clock := time.Unix(0, 0)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: 10,
		Burst:             10,
		IdleTTL:           time.Minute,
		Now:               func() time.Time { return clock },
	})
	defer rl.Close()

	for i := 0; i < 500; i++ {
		rl.Allow(string(rune('a'+i%26)) + time.Duration(i).String())
	}
	if rl.Buckets() == 0 {
		t.Fatal("no buckets were tracked")
	}

	clock = clock.Add(2 * time.Minute)
	rl.evict()

	if got := rl.Buckets(); got != 0 {
		t.Fatalf("Buckets = %d after the idle TTL elapsed, want 0", got)
	}
}

func TestRateLimitMiddlewareReturns429WithRetryAfter(t *testing.T) {
	clock := time.Unix(0, 0)
	rl := NewRateLimiter(RateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		Now:               func() time.Time { return clock },
	})
	defer rl.Close()

	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpx.RequestID(),
		rl.Middleware(RateLimitKey),
	)

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("429 response has no Retry-After header")
	}
	if ct := second.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}
