package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/resilience"
	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixture wires a gateway against two stub upstreams.
type fixture struct {
	handler http.Handler
	catalog *httptest.Server
	scorer  *httptest.Server
}

func newFixture(t *testing.T, catalog, scorer http.Handler, degrade bool) *fixture {
	t.Helper()

	catalogSrv := httptest.NewServer(catalog)
	scorerSrv := httptest.NewServer(scorer)
	t.Cleanup(catalogSrv.Close)
	t.Cleanup(scorerSrv.Close)

	m := telemetry.NewMetrics("gateway", "test", "test")
	logger := testLogger()

	// One attempt and a high trip threshold keep these tests about handler
	// behaviour rather than about retry timing.
	fast := httpx.UpstreamConfig{
		Timeout: 2 * time.Second,
		Retry:   resilience.RetryConfig{MaxAttempts: 1},
		Breaker: resilience.BreakerConfig{MinimumRequests: 1000},
	}

	catalogCfg, scorerCfg := fast, fast
	catalogCfg.Name, catalogCfg.BaseURL = "catalog", catalogSrv.URL
	scorerCfg.Name, scorerCfg.BaseURL = "scorer", scorerSrv.URL

	h := NewHandler(Upstreams{
		Catalog: httpx.NewUpstream(catalogCfg, m, logger),
		Scorer:  httpx.NewUpstream(scorerCfg, m, logger),
	}, logger, degrade)

	return &fixture{
		handler: httpx.Chain(h.Routes(), httpx.RequestID(), httpx.Logging(logger)),
		catalog: catalogSrv,
		scorer:  scorerSrv,
	}
}

func (f *fixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func okCatalog(deviceID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": deviceID, "name": "pump-01", "site": "west"})
	})
}

func okScorer(score float64, anomaly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"score": score, "anomaly": anomaly})
	})
}

func TestScoreReadingReturnsEnrichedResult(t *testing.T) {
	f := newFixture(t, okCatalog("dev-1"), okScorer(0.91, true), true)

	rec := f.post(t, "/api/v1/readings/score", `{"device_id":"dev-1","readings":[1,2,3]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	var got ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Score != 0.91 || !got.Anomaly {
		t.Fatalf("score = %v anomaly = %v, want 0.91 true", got.Score, got.Anomaly)
	}
	if got.Degraded {
		t.Fatal("Degraded = true when both upstreams were healthy")
	}
	if len(got.Device) == 0 {
		t.Fatal("device enrichment was dropped despite a healthy catalog")
	}
}

func TestScoreReadingDegradesWhenCatalogIsDown(t *testing.T) {
	// This is the central availability property: a non-essential dependency
	// failing must not take down the essential path.
	brokenCatalog := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	f := newFixture(t, brokenCatalog, okScorer(0.42, false), true)

	rec := f.post(t, "/api/v1/readings/score", `{"device_id":"dev-1","readings":[1,2,3]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: scoring must survive a catalog outage. body=%s", rec.Code, rec.Body)
	}

	var got ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Degraded {
		t.Fatal("Degraded = false; a client cannot tell this result was incomplete")
	}
	if got.Score != 0.42 {
		t.Fatalf("score = %v, want 0.42", got.Score)
	}
	if len(got.Device) != 0 {
		t.Fatal("device block was populated despite catalog being down")
	}
}

func TestScoreReadingFailsClosedWhenDegradationDisabled(t *testing.T) {
	brokenCatalog := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	f := newFixture(t, brokenCatalog, okScorer(0.42, false), false)

	rec := f.post(t, "/api/v1/readings/score", `{"device_id":"dev-1","readings":[1,2,3]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when degradation is turned off", rec.Code)
	}
}

func TestScoreReadingFailsWhenScorerIsDown(t *testing.T) {
	// Unlike catalog, there is no partial answer worth returning here.
	brokenScorer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	f := newFixture(t, okCatalog("dev-1"), brokenScorer, true)

	rec := f.post(t, "/api/v1/readings/score", `{"device_id":"dev-1","readings":[1,2,3]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
}

func TestScoreReadingReturns404ForUnknownDevice(t *testing.T) {
	missing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	f := newFixture(t, missing, okScorer(0.1, false), true)

	rec := f.post(t, "/api/v1/readings/score", `{"device_id":"nope","readings":[1]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: a reachable catalog reporting 404 is a client error", rec.Code)
	}
}

func TestScoreReadingRejectsBadInput(t *testing.T) {
	f := newFixture(t, okCatalog("dev-1"), okScorer(0.5, false), true)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"not json", `{`},
		{"missing device", `{"readings":[1,2]}`},
		{"no readings", `{"device_id":"dev-1","readings":[]}`},
		{"unknown field", `{"device_id":"dev-1","readings":[1],"sneaky":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.post(t, "/api/v1/readings/score", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestScoreReadingRejectsOversizedReadings(t *testing.T) {
	f := newFixture(t, okCatalog("dev-1"), okScorer(0.5, false), true)

	readings := make([]string, maxReadings+1)
	for i := range readings {
		readings[i] = "1"
	}
	body := `{"device_id":"dev-1","readings":[` + strings.Join(readings, ",") + `]}`

	rec := f.post(t, "/api/v1/readings/score", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: an unbounded array is a memory and CPU amplification vector", rec.Code)
	}
}

func TestUpstreamErrorBodyIsNotLeaked(t *testing.T) {
	// A downstream 5xx must not put its internals in a public response.
	leaky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"pq: password authentication failed for user \"app\" on host db.internal"}`))
	})
	f := newFixture(t, leaky, okScorer(0.5, false), false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, leak := range []string{"password", "db.internal", "pq:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaked upstream internals (%q): %s", leak, body)
		}
	}
}

func TestCorrelationHeadersAreForwarded(t *testing.T) {
	var gotRequestID atomic.Value
	gotRequestID.Store("")

	catalog := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID.Store(r.Header.Get(httpx.RequestIDHeader))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1"}`))
	})
	f := newFixture(t, catalog, okScorer(0.5, false), true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/dev-1", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	issued := rec.Header().Get(httpx.RequestIDHeader)
	if issued == "" {
		t.Fatal("no request id was issued to the client")
	}
	if forwarded := gotRequestID.Load().(string); forwarded != issued {
		t.Fatalf("catalog saw request id %q, client got %q; a trace cannot be correlated across the hop", forwarded, issued)
	}
}

func TestClientCredentialIsNotForwardedUpstream(t *testing.T) {
	var sawAuth atomic.Value
	sawAuth.Store("")

	catalog := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1"}`))
	})
	f := newFixture(t, catalog, okScorer(0.5, false), true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/dev-1", nil)
	req.Header.Set("Authorization", "Bearer super-secret-client-key")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if got := sawAuth.Load().(string); got != "" {
		t.Fatalf("catalog received the caller's credential (%q); headers must be an allowlist", got)
	}
}

func TestUnroutedPathsGet404(t *testing.T) {
	f := newFixture(t, okCatalog("dev-1"), okScorer(0.5, false), true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWrongMethodGets405(t *testing.T) {
	f := newFixture(t, okCatalog("dev-1"), okScorer(0.5, false), true)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
