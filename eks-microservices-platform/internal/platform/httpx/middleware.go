// Package httpx holds the HTTP server plumbing shared by the Go services:
// middleware, error responses, a resilient client and a server that drains
// cleanly on SIGTERM.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/telemetry"
	"go.opentelemetry.io/otel/trace"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyLogger
)

// RequestIDHeader is the correlation header accepted from clients and echoed on
// every response.
const RequestIDHeader = "X-Request-Id"

// Middleware decorates an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first argument is the outermost layer,
// which is the order they appear to a reader of the setup code.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// RequestID attaches a correlation ID to the context and the response.
//
// An inbound ID is trusted only if it looks like an ID we issued. Echoing
// arbitrary client input into logs invites log injection and unbounded
// cardinality if it ever reaches a metric label.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func validRequestID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable in a meaningful way here, and
		// a timestamp-derived ID still correlates a single request's log lines.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// RequestIDFrom returns the correlation ID for a request context, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}

// LoggerFrom returns the request-scoped logger, falling back to the default.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Logging emits one structured line per request and puts a pre-tagged logger on
// the context.
//
// The trace and span IDs are included so a log line found in Loki links
// straight to its trace, which is the difference between "we have traces" and
// "traces are usable during an incident".
func Logging(base *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			attrs := []any{
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			}
			if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
				attrs = append(attrs,
					slog.String("trace_id", sc.TraceID().String()),
					slog.String("span_id", sc.SpanID().String()),
				)
			}
			logger := base.With(attrs...)

			ctx := context.WithValue(r.Context(), ctxKeyLogger, logger)
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			// Client errors are the caller's problem and server errors are
			// ours; logging both at the same level makes the 5xx signal
			// impossible to alert on.
			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}

			logger.LogAttrs(r.Context(), level, "http_request",
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.written),
				slog.Duration("duration", time.Since(start)),
				slog.String("route", routeOf(r)),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// Recovery converts a handler panic into a 500 instead of letting it kill the
// connection, and logs the stack with the request's correlation ID.
func Recovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					// http.ErrAbortHandler is the documented way to abort a
					// response on purpose; re-panicking preserves that.
					if v == http.ErrAbortHandler {
						panic(v)
					}
					LoggerFrom(r.Context()).Error("panic recovered",
						slog.Any("panic", v),
						slog.String("stack", stack()),
					)
					WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "The service failed to handle this request.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Metrics records the RED signals for every request.
func Metrics(m *telemetry.Metrics) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.RequestsInFlight.Inc()
			defer m.RequestsInFlight.Dec()

			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			route := routeOf(r)
			status := strconv.Itoa(rec.status)

			m.RequestsTotal.WithLabelValues(r.Method, route, status, statusClass(rec.status)).Inc()
			m.RequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
			m.ResponseSize.WithLabelValues(route).Observe(float64(rec.written))
		})
	}
}

// Timeout bounds handler execution so one slow dependency cannot pin a worker
// indefinitely.
//
// It sets the deadline on the request context rather than using
// http.TimeoutHandler, because a handler that respects its context can stop the
// downstream work it started; TimeoutHandler only discards the response and
// leaves the work running.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			done := make(chan struct{})
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			go func() {
				defer close(done)
				next.ServeHTTP(rec, r.WithContext(ctx))
			}()

			select {
			case <-done:
			case <-ctx.Done():
				// Only synthesise a timeout response if the handler has not
				// already begun writing; otherwise the bytes are on the wire.
				if rec.markTimedOut() {
					WriteProblem(w, r, http.StatusGatewayTimeout, "timeout", "The request exceeded its time budget.")
				}
			}
		})
	}
}

// SecurityHeaders sets the response headers that cost nothing and close off a
// class of browser-side attacks.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBodyBytes rejects oversized request bodies before they are read into
// memory, so a single client cannot drive the pod into an OOMKill.
func MaxBodyBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// routeOf returns the matched mux pattern, which is bounded by the number of
// registered routes. It falls back to a constant rather than the raw path so an
// unmatched request can never introduce a new metric label value.
func routeOf(r *http.Request) string {
	if p := r.Pattern; p != "" {
		return p
	}
	return "unmatched"
}

func statusClass(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
