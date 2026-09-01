// Package health implements the three probe semantics Kubernetes actually
// distinguishes, which most charts collapse into one endpoint pointed at "/".
//
//	startup   — "has the process finished booting?" Gates the other two probes
//	            so a slow boot (migrations, cache warm) is not mistaken for a
//	            hung process and restarted in a loop.
//	liveness  — "is this process wedged?" Deliberately checks nothing external.
//	            A liveness probe that fails when a dependency is down turns a
//	            database blip into a cluster-wide restart storm.
//	readiness — "should this pod receive traffic right now?" Checks dependencies
//	            and flips to false the instant a drain begins.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Check reports whether one dependency is usable. It must respect ctx and
// return quickly; a probe handler that blocks is indistinguishable from a
// wedged process.
type Check func(ctx context.Context) error

// Status is the JSON body returned by the probe endpoints.
type Status struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Registry tracks boot state, drain state and readiness dependencies.
//
// The zero value is not usable; call New.
type Registry struct {
	service string
	version string
	timeout time.Duration

	mu       sync.RWMutex
	checks   map[string]Check
	started  bool
	draining bool
}

// New returns a Registry for a service that has not yet finished booting.
// perCheckTimeout bounds each individual readiness check.
func New(service, version string, perCheckTimeout time.Duration) *Registry {
	if perCheckTimeout <= 0 {
		perCheckTimeout = 2 * time.Second
	}
	return &Registry{
		service: service,
		version: version,
		timeout: perCheckTimeout,
		checks:  make(map[string]Check),
	}
}

// Register adds a readiness dependency. Call it during boot, before MarkStarted.
func (r *Registry) Register(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// MarkStarted records that boot finished. Until it is called the startup probe
// fails, which holds off liveness and readiness.
func (r *Registry) MarkStarted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = true
}

// BeginDrain makes readiness fail while leaving liveness passing.
//
// This is the step that makes a rolling update lossless. The endpoints
// controller needs time to observe the NotReady state and pull this pod out of
// every kube-proxy/ALB target list; until it does, in-flight requests keep
// arriving. Callers should sleep for that propagation delay after calling this
// and before shutting the listener down.
func (r *Registry) BeginDrain() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.draining = true
}

// Draining reports whether a drain has begun.
func (r *Registry) Draining() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.draining
}

func (r *Registry) snapshot() (started, draining bool, checks map[string]Check) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	checks = make(map[string]Check, len(r.checks))
	for k, v := range r.checks {
		checks[k] = v
	}
	return r.started, r.draining, checks
}

// StartupHandler reports 200 once boot has completed.
func (r *Registry) StartupHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started, _, _ := r.snapshot()
		if !started {
			write(w, http.StatusServiceUnavailable, Status{Status: "starting", Service: r.service, Version: r.version})
			return
		}
		write(w, http.StatusOK, Status{Status: "ok", Service: r.service, Version: r.version})
	})
}

// LivenessHandler reports 200 whenever the process can still serve HTTP.
//
// It intentionally ignores dependency health and drain state: a draining pod is
// alive, and restarting it would abandon the requests it is trying to finish.
func (r *Registry) LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		write(w, http.StatusOK, Status{Status: "ok", Service: r.service, Version: r.version})
	})
}

// ReadinessHandler reports 200 only when boot finished, no drain is in progress
// and every registered dependency check passes.
func (r *Registry) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started, draining, checks := r.snapshot()
		switch {
		case !started:
			write(w, http.StatusServiceUnavailable, Status{Status: "starting", Service: r.service, Version: r.version})
			return
		case draining:
			write(w, http.StatusServiceUnavailable, Status{Status: "draining", Service: r.service, Version: r.version})
			return
		}

		results := make(map[string]string, len(checks))
		healthy := true

		// Run checks concurrently so readiness latency is bounded by the
		// slowest dependency rather than their sum.
		var wg sync.WaitGroup
		var mu sync.Mutex
		for name, check := range checks {
			wg.Add(1)
			go func(name string, check Check) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(req.Context(), r.timeout)
				defer cancel()

				msg := "ok"
				if err := check(ctx); err != nil {
					msg = err.Error()
				}

				mu.Lock()
				defer mu.Unlock()
				results[name] = msg
				if msg != "ok" {
					healthy = false
				}
			}(name, check)
		}
		wg.Wait()

		status := Status{Status: "ok", Service: r.service, Version: r.version, Checks: results}
		if !healthy {
			status.Status = "degraded"
			write(w, http.StatusServiceUnavailable, status)
			return
		}
		write(w, http.StatusOK, status)
	})
}

// Names returns the registered check names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.checks))
	for name := range r.checks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func write(w http.ResponseWriter, code int, body Status) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
