package catalog

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

// Handler serves the catalog API.
type Handler struct {
	repo   Repository
	logger *slog.Logger
}

// NewHandler builds the catalog handler.
func NewHandler(repo Repository, logger *slog.Logger) *Handler {
	return &Handler{repo: repo, logger: logger}
}

// Routes registers the catalog API.
//
// The paths are unversioned-internal (/v1/...) rather than the public
// /api/v1/... the gateway exposes, so the public contract can be reshaped at
// the edge without renaming internal routes.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/devices", h.List)
	mux.HandleFunc("POST /v1/devices", h.Create)
	mux.HandleFunc("GET /v1/devices/{id}", h.Get)
	return mux
}

// Get returns a single device.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "A device id is required.")
		return
	}

	device, err := h.repo.Get(r.Context(), id)
	if err != nil {
		h.writeRepoError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, device)
}

// List returns a page of devices.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	opts := ListOptions{
		Site:   q.Get("site"),
		Cursor: q.Get("cursor"),
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "limit must be an integer.")
			return
		}
		if limit < 0 {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "limit must not be negative.")
			return
		}
		opts.Limit = limit
	}

	page, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.writeRepoError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// Create registers a device.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var device Device
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&device); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_request", "The request body is not a valid device.")
		return
	}

	// Server-assigned fields are ignored rather than rejected, so a client can
	// round-trip a device it previously read without stripping them first.
	device.CreatedAt, device.UpdatedAt = zeroTime, zeroTime

	created, err := h.repo.Create(r.Context(), device)
	if err != nil {
		h.writeRepoError(w, r, err)
		return
	}

	w.Header().Set("Location", "/v1/devices/"+created.ID)
	httpx.WriteJSON(w, http.StatusCreated, created)
}

// writeRepoError maps domain errors onto status codes.
//
// Only the sentinel errors produce a detail message; anything unrecognised is a
// 500 with a generic body, because an unclassified error is exactly the kind
// that carries a SQL fragment or a hostname.
func (h *Handler) writeRepoError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "device_not_found", "No device is registered with that id.")
	case errors.Is(err, ErrAlreadyExists):
		httpx.WriteProblem(w, r, http.StatusConflict, "device_exists", "A device with that id is already registered.")
	case errors.Is(err, ErrInvalid):
		// Validation messages are authored in device.go and safe to return.
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_device", strings.TrimPrefix(err.Error(), "device is invalid: "))
	default:
		httpx.LoggerFrom(r.Context()).Error("repository call failed", slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
}

// zeroTime is the value server-assigned timestamps are reset to before insert.
var zeroTime = time.Time{}
