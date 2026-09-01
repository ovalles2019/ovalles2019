package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/resilience"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
)

func newTestUpstream(t *testing.T, url string, retry resilience.RetryConfig, breaker resilience.BreakerConfig) *Upstream {
	t.Helper()
	return NewUpstream(UpstreamConfig{
		Name:    "dep",
		BaseURL: url,
		Timeout: 2 * time.Second,
		Retry:   retry,
		Breaker: breaker,
	}, telemetry.NewMetrics("test", "v0", "test"), quietLogger())
}

// noWait replaces the retry sleep so tests do not pay real backoff.
func noWait(context.Context, time.Duration) error { return nil }

func TestUpstreamRetriesServerErrorsThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 5, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 100},
	)

	resp, err := up.Get(context.Background(), "/thing", nil)
	if err != nil {
		t.Fatalf("err = %v, want nil after the upstream recovered", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestUpstreamDoesNotRetryClientErrors(t *testing.T) {
	// A 400 will be a 400 every time; retrying only multiplies load.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 5, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 100},
	)

	resp, err := up.Get(context.Background(), "/thing", nil)
	if err != nil {
		t.Fatalf("err = %v; a 4xx should be returned, not raised", err)
	}
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Status)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1: a 4xx must not be retried", got)
	}
}

func TestUpstreamClientErrorsDoNotTripTheBreaker(t *testing.T) {
	// Callers sending malformed requests say nothing about dependency health;
	// counting their 400s would let one bad client cut everyone else off.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 1, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 5, FailureRatio: 0.5},
	)

	for i := 0; i < 50; i++ {
		if _, err := up.Get(context.Background(), "/thing", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := up.BreakerState(); got != resilience.StateClosed {
		t.Fatalf("breaker = %v, want closed", got)
	}
}

func TestUpstreamBreakerOpensAndShedsLoad(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 1, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 5, FailureRatio: 0.5, OpenTimeout: time.Hour},
	)

	for i := 0; i < 5; i++ {
		_, _ = up.Get(context.Background(), "/thing", nil)
	}
	if got := up.BreakerState(); got != resilience.StateOpen {
		t.Fatalf("breaker = %v, want open", got)
	}

	before := calls.Load()
	_, err := up.Get(context.Background(), "/thing", nil)
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
	if after := calls.Load(); after != before {
		t.Fatalf("the open breaker still forwarded %d calls to a failing dependency", after-before)
	}
}

func TestUpstreamRetryResendsTheRequestBody(t *testing.T) {
	// A retried POST with a drained reader would arrive empty and turn a
	// transient blip into a confusing 400.
	var bodies []string
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 3, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 100},
	)

	payload := []byte(`{"device_id":"dev-1"}`)
	if _, err := up.Post(context.Background(), "/thing", payload, nil); err != nil {
		t.Fatalf("err = %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("saw %d requests, want 2", len(bodies))
	}
	for i, b := range bodies {
		if b != string(payload) {
			t.Fatalf("attempt %d body = %q, want %q", i+1, b, payload)
		}
	}
}

func TestUpstreamPropagatesRequestID(t *testing.T) {
	var got atomic.Value
	got.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get(RequestIDHeader))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 1, Sleep: noWait},
		resilience.BreakerConfig{MinimumRequests: 100},
	)

	ctx := context.WithValue(context.Background(), ctxKeyRequestID, "0123456789abcdef0123456789abcdef")
	if _, err := up.Get(ctx, "/thing", nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if v := got.Load().(string); v != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("upstream saw request id %q; correlation is broken across the hop", v)
	}
}

func TestUpstreamRespectsCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	up := newTestUpstream(t, srv.URL,
		resilience.RetryConfig{MaxAttempts: 5, BaseDelay: 10 * time.Millisecond},
		resilience.BreakerConfig{MinimumRequests: 100},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := up.Get(ctx, "/slow", nil); err == nil {
		t.Fatal("err = nil, want a deadline failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("call took %v; retries ran past the caller's 100ms deadline", elapsed)
	}
}

func TestUpstreamRecordsMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := telemetry.NewMetrics("test", "v0", "test")
	up := NewUpstream(UpstreamConfig{
		Name:    "dep",
		BaseURL: srv.URL,
		Retry:   resilience.RetryConfig{MaxAttempts: 1},
		Breaker: resilience.BreakerConfig{MinimumRequests: 100},
	}, m, quietLogger())

	if _, err := up.Get(context.Background(), "/thing", nil); err != nil {
		t.Fatalf("err = %v", err)
	}

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, f := range families {
		if f.GetName() == "upstream_requests_total" {
			for _, metric := range f.GetMetric() {
				for _, lp := range metric.GetLabel() {
					if lp.GetName() == "outcome" && lp.GetValue() == "success" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("no successful upstream_requests_total series was recorded")
	}
}
