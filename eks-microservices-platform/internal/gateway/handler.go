package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

// Upstreams are the dependencies the gateway fans out to.
type Upstreams struct {
	Catalog *httpx.Upstream
	Scorer  *httpx.Upstream
}

// Handler serves the public API.
type Handler struct {
	up     Upstreams
	logger *slog.Logger
	// degradeOnCatalogFailure controls whether a scoring request still succeeds
	// when device enrichment is unavailable. See ScoreReading.
	degradeOnCatalogFailure bool
}

// NewHandler builds the gateway handler.
func NewHandler(up Upstreams, logger *slog.Logger, degradeOnCatalogFailure bool) *Handler {
	return &Handler{up: up, logger: logger, degradeOnCatalogFailure: degradeOnCatalogFailure}
}

// Routes registers the public API on a mux.
//
// Method-qualified patterns (Go 1.22+) give exact-match routing and a stable
// r.Pattern for the metrics route label, instead of the prefix matching that
// makes "/" quietly swallow every unmatched path.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/devices", h.ListDevices)
	mux.HandleFunc("POST /api/v1/devices", h.CreateDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", h.GetDevice)
	mux.HandleFunc("POST /api/v1/readings/score", h.ScoreReading)
	mux.HandleFunc("GET /api/v1/dependencies", h.Dependencies)
	return mux
}

// ScoreRequest is the public scoring payload.
type ScoreRequest struct {
	DeviceID string    `json:"device_id"`
	Readings []float64 `json:"readings"`
}

// ScoreResponse is the public scoring result.
type ScoreResponse struct {
	DeviceID string  `json:"device_id"`
	Score    float64 `json:"score"`
	Anomaly  bool    `json:"anomaly"`
	// Device is nil when catalog enrichment was skipped or unavailable.
	Device json.RawMessage `json:"device,omitempty"`
	// Degraded reports that the answer is correct but less complete than usual,
	// so a client can tell a full result from a partial one rather than
	// silently treating them the same.
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
}

// ScoreReading enriches a reading with device metadata and scores it.
//
// This is the platform's one genuine fan-out, and the interesting decision is
// what to do when catalog is down but scorer is healthy. Failing the whole
// request would let an outage in a non-essential enrichment path take down the
// essential scoring path — the classic way a microservice split makes
// availability worse than the monolith it replaced. So by default the request
// succeeds with Degraded=true and no device block.
//
// Scorer being unavailable is different: there is no answer to give, so that is
// a real 503.
func (h *Handler) ScoreReading(w http.ResponseWriter, r *http.Request) {
	var req ScoreRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.DeviceID == "" {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "device_id is required.")
		return
	}
	if len(req.Readings) == 0 {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "readings must contain at least one value.")
		return
	}
	if len(req.Readings) > maxReadings {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("readings must contain at most %d values.", maxReadings))
		return
	}

	logger := httpx.LoggerFrom(r.Context())
	resp := ScoreResponse{DeviceID: req.DeviceID}

	// Enrichment first: a device the catalog has never heard of is a client
	// error worth reporting, but only when the catalog is actually reachable.
	device, enrichErr := h.fetchDevice(r, req.DeviceID)
	switch {
	case enrichErr == nil:
		resp.Device = device
	case errors.Is(enrichErr, errDeviceNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "device_not_found",
			"No device is registered with that device_id.")
		return
	case !h.degradeOnCatalogFailure:
		logger.Error("catalog enrichment failed and degradation is disabled", slog.Any("error", enrichErr))
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "catalog_unavailable",
			"Device metadata is temporarily unavailable.")
		return
	default:
		logger.Warn("serving degraded score without device enrichment", slog.Any("error", enrichErr))
		resp.Degraded = true
		resp.Reason = "device metadata unavailable"
	}

	body, err := json.Marshal(map[string]any{
		"device_id": req.DeviceID,
		"readings":  req.Readings,
	})
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Failed to build the scoring request.")
		return
	}

	scored, err := h.up.Scorer.Post(r.Context(), "/v1/score", body, forwardHeaders(r))
	if err != nil {
		logger.Error("scorer call failed", slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "scorer_unavailable",
			"Scoring is temporarily unavailable. Retry after a short backoff.")
		return
	}
	if scored.Status >= 400 {
		relayUpstreamError(w, r, "scorer", scored)
		return
	}

	var scorerResult struct {
		Score   float64 `json:"score"`
		Anomaly bool    `json:"anomaly"`
	}
	if err := json.Unmarshal(scored.Body, &scorerResult); err != nil {
		logger.Error("scorer returned an unparseable body", slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusBadGateway, "invalid_upstream_response",
			"The scoring service returned an unexpected response.")
		return
	}

	resp.Score = scorerResult.Score
	resp.Anomaly = scorerResult.Anomaly
	httpx.WriteJSON(w, http.StatusOK, resp)
}

var errDeviceNotFound = errors.New("device not found")

func (h *Handler) fetchDevice(r *http.Request, deviceID string) (json.RawMessage, error) {
	resp, err := h.up.Catalog.Get(r.Context(), "/v1/devices/"+deviceID, forwardHeaders(r))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.Status == http.StatusNotFound:
		return nil, errDeviceNotFound
	case resp.Status >= 400:
		return nil, fmt.Errorf("catalog returned %d", resp.Status)
	}
	return json.RawMessage(resp.Body), nil
}

// ListDevices proxies the catalog listing.
func (h *Handler) ListDevices(w http.ResponseWriter, r *http.Request) {
	path := "/v1/devices"
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	h.proxy(w, r, h.up.Catalog, http.MethodGet, path, nil)
}

// GetDevice proxies a single-device lookup.
func (h *Handler) GetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "A device id is required.")
		return
	}
	h.proxy(w, r, h.up.Catalog, http.MethodGet, "/v1/devices/"+id, nil)
}

// CreateDevice proxies device registration.
func (h *Handler) CreateDevice(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "The request body could not be read.")
		return
	}
	h.proxy(w, r, h.up.Catalog, http.MethodPost, "/v1/devices", body)
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, up *httpx.Upstream, method, path string, body []byte) {
	var resp *httpx.Response
	var err error

	switch method {
	case http.MethodGet:
		resp, err = up.Get(r.Context(), path, forwardHeaders(r))
	default:
		resp, err = up.Post(r.Context(), path, body, forwardHeaders(r))
	}

	if err != nil {
		httpx.LoggerFrom(r.Context()).Error("upstream call failed",
			slog.String("upstream", up.Name()),
			slog.Any("error", err),
		)
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "upstream_unavailable",
			"A required service is temporarily unavailable. Retry after a short backoff.")
		return
	}
	if resp.Status >= 400 {
		relayUpstreamError(w, r, up.Name(), resp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

// relayUpstreamError forwards an upstream 4xx without leaking its internals.
//
// Only the status is trusted. Copying an upstream body through verbatim is how
// internal hostnames, SQL fragments and stack traces end up in a public
// response.
func relayUpstreamError(w http.ResponseWriter, r *http.Request, upstream string, resp *httpx.Response) {
	var problem httpx.Problem
	code := "upstream_error"
	detail := "The request was rejected by a downstream service."

	if err := json.Unmarshal(resp.Body, &problem); err == nil && problem.Code != "" {
		code = problem.Code
		if problem.Detail != "" {
			detail = problem.Detail
		}
	}

	status := resp.Status
	if status >= 500 {
		// A downstream 5xx is this platform's failure, not the caller's.
		status = http.StatusBadGateway
		code = "upstream_error"
		detail = "A downstream service failed to handle the request."
	}

	httpx.LoggerFrom(r.Context()).Warn("relaying upstream error",
		slog.String("upstream", upstream),
		slog.Int("upstream_status", resp.Status),
	)
	httpx.WriteProblem(w, r, status, code, detail)
}

// DependencyStatus reports one dependency's breaker state.
type DependencyStatus struct {
	Name    string `json:"name"`
	Breaker string `json:"breaker"`
}

// Dependencies exposes breaker state, so an operator can tell "the gateway is
// refusing calls" apart from "the dependency is down" without reading metrics.
func (h *Handler) Dependencies(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"dependencies": []DependencyStatus{
			{Name: h.up.Catalog.Name(), Breaker: h.up.Catalog.BreakerState().String()},
			{Name: h.up.Scorer.Name(), Breaker: h.up.Scorer.BreakerState().String()},
		},
	})
}

const (
	maxReadings  = 4096
	maxBodyBytes = 1 << 20
)

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	// Reject unknown fields so a client typo fails loudly instead of being
	// silently dropped and producing a puzzling result.
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("request body is not valid JSON: %w", err)
	}
	return nil
}

// forwardHeaders propagates correlation context to downstream services.
//
// It is an allowlist. Forwarding the inbound Authorization header would hand
// the caller's credential to every internal service; each hop authenticates on
// its own terms instead.
func forwardHeaders(r *http.Request) map[string]string {
	h := map[string]string{}
	if id := httpx.RequestIDFrom(r.Context()); id != "" {
		h[httpx.RequestIDHeader] = id
	}
	if client := ClientFrom(r.Context()); client != "" {
		h["X-Client-Id"] = client
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		h["Idempotency-Key"] = key
	}
	return h
}

// DefaultRequestTimeout bounds a single inbound request end to end.
const DefaultRequestTimeout = 10 * time.Second
