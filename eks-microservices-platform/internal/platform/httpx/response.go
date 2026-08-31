package httpx

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sync"
)

// Problem is an RFC 7807 error body.
//
// A single machine-readable error shape across every service means clients (and
// the gateway's own upstream classification) can branch on Code without parsing
// prose, and lets error-rate dashboards group by a stable field.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteProblem renders an error as application/problem+json.
//
// detail is written verbatim, so callers must pass an operator-authored string
// and never a raw upstream error: internal errors leak connection strings,
// hostnames and query fragments to the client.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	}
	if r != nil {
		p.RequestID = RequestIDFrom(r.Context())
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteJSON renders v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// responseRecorder captures the status and byte count for logging and metrics,
// and guards against a double write when the timeout middleware fires.
type responseRecorder struct {
	http.ResponseWriter

	mu          sync.Mutex
	status      int
	written     int64
	wroteHeader bool
	timedOut    bool
}

func (r *responseRecorder) WriteHeader(code int) {
	r.mu.Lock()
	if r.wroteHeader || r.timedOut {
		r.mu.Unlock()
		return
	}
	r.wroteHeader = true
	r.status = code
	r.mu.Unlock()

	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	if r.timedOut {
		r.mu.Unlock()
		// The timeout middleware already owns the response; report the write as
		// accepted so the handler unwinds normally instead of erroring on a
		// connection that is no longer its concern.
		return len(b), nil
	}
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = http.StatusOK
		r.mu.Unlock()
		r.ResponseWriter.WriteHeader(http.StatusOK)
	} else {
		r.mu.Unlock()
	}

	n, err := r.ResponseWriter.Write(b)

	r.mu.Lock()
	r.written += int64(n)
	r.mu.Unlock()
	return n, err
}

// Flush forwards to the underlying writer when it supports streaming.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// markTimedOut claims the response for the timeout middleware. It returns false
// if the handler already started writing, in which case the timeout must not
// append a second body.
func (r *responseRecorder) markTimedOut() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wroteHeader {
		return false
	}
	r.timedOut = true
	return true
}

// Status returns the recorded status code.
func (r *responseRecorder) Status() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func stack() string {
	buf := make([]byte, 8<<10)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}
