package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}), RequestID())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == "" {
		t.Fatal("no request id reached the handler")
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Fatalf("response header %q does not match handler value %q", got, seen)
	}
}

func TestRequestIDAcceptsWellFormedInboundValue(t *testing.T) {
	inbound := "0123456789abcdef0123456789abcdef"
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}), RequestID())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, inbound)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen != inbound {
		t.Fatalf("request id = %q, want the caller's %q so a trace spans the whole call", seen, inbound)
	}
}

func TestRequestIDRejectsHostileInboundValue(t *testing.T) {
	// An id echoed into logs must never carry newlines or control characters,
	// or an attacker can forge log entries.
	hostile := []string{
		"not-hex-at-all-not-hex-at-all-xx",
		"short",
		strings.Repeat("a", 4096),
		"abc\ndef INJECTED log line",
		"../../etc/passwd",
	}
	for _, id := range hostile {
		t.Run(id[:min(12, len(id))], func(t *testing.T) {
			var seen string
			h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = RequestIDFrom(r.Context())
			}), RequestID())

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(RequestIDHeader, id)
			h.ServeHTTP(httptest.NewRecorder(), req)

			if seen == id {
				t.Fatalf("hostile request id %q was accepted verbatim", id)
			}
			if !validRequestID(seen) {
				t.Fatalf("replacement id %q is not well formed", seen)
			}
		})
	}
}

func TestRecoveryConvertsPanicToProblemJSON(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler exploded")
	}), RequestID(), Logging(quietLogger()), Recovery())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not problem+json: %v", err)
	}
	if strings.Contains(rec.Body.String(), "handler exploded") {
		t.Fatal("the panic value was leaked to the client")
	}
	if p.RequestID == "" {
		t.Error("the 500 carries no request id, so the client cannot quote one in a support request")
	}
}

func TestTimeoutReturns504WhenHandlerOverruns(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), RequestID(), Timeout(30*time.Millisecond))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
}

func TestTimeoutCancelsHandlerContext(t *testing.T) {
	// The point of putting the deadline on the context is that the handler can
	// abandon the work it started, not merely that the client gets a 504.
	cancelled := make(chan struct{})
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	}), RequestID(), Timeout(20*time.Millisecond))

	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was never cancelled; downstream work would keep running past the deadline")
	}
}

func TestTimeoutDoesNotCorruptAnAlreadyWrittenResponse(t *testing.T) {
	// A handler that answered quickly then kept working must not have a 504
	// appended to its body.
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
		<-r.Context().Done()
	}), RequestID(), Timeout(30*time.Millisecond))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 to be preserved", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"ok":true`) || strings.Contains(body, "timeout") {
		t.Fatalf("body was corrupted by the timeout path: %s", body)
	}
}

func TestTimeoutPassesThroughFastHandlers(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}), RequestID(), Timeout(time.Second))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMetricsLabelsUseRoutePatternNotRawPath(t *testing.T) {
	// The cardinality guarantee: a thousand distinct ids must produce one
	// series, not a thousand.
	m := telemetry.NewMetrics("test", "v0", "test")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Chain(mux, RequestID(), Metrics(m))

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/device-"+strings.Repeat("x", i%7)+string(rune('a'+i%26)), nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var series int
	var labelValue string
	for _, f := range families {
		if f.GetName() != "http_requests_total" {
			continue
		}
		series = len(f.GetMetric())
		for _, lp := range f.GetMetric()[0].GetLabel() {
			if lp.GetName() == "route" {
				labelValue = lp.GetValue()
			}
		}
	}

	if series != 1 {
		t.Fatalf("http_requests_total has %d series after 100 distinct paths; the route label is unbounded", series)
	}
	if labelValue != "GET /api/v1/devices/{id}" {
		t.Fatalf("route label = %q, want the mux pattern", labelValue)
	}
}

func TestMetricsRecordsStatusClass(t *testing.T) {
	m := telemetry.NewMetrics("test", "v0", "test")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := Chain(mux, RequestID(), Metrics(m))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))

	families, _ := m.Registry().Gather()
	found := false
	for _, f := range families {
		if f.GetName() != "http_requests_total" {
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "status_class" && lp.GetValue() == "5xx" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("no 5xx status_class series; the SLO error-rate query has nothing to sum")
	}
}

func TestMaxBodyBytesRejectsOversizedRequests(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteProblem(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Body too large.")
			return
		}
		w.WriteHeader(http.StatusOK)
	}), RequestID(), MaxBodyBytes(64))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 4096)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), SecurityHeaders())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestChainAppliesOutermostFirst(t *testing.T) {
	var order []string
	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), mark("first"), mark("second"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestWriteProblemDoesNotLeakInternals(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyRequestID, "abc"))

	WriteProblem(rec, req, http.StatusBadRequest, "invalid_request", "The device_id field is required.")

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Code != "invalid_request" || p.Status != http.StatusBadRequest || p.RequestID != "abc" {
		t.Fatalf("problem = %+v", p)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
