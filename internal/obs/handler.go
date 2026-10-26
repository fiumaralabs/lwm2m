// Package obs provides daemon health probes and scrape-time metrics.
package obs

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

// Checker optionally verifies dependencies needed for readiness.
type Checker interface{ Check(context.Context) error }

// Handler serves the daemon's observability endpoints.
type Handler struct {
	checker Checker
	count   func() int
	metrics bool
	ready   atomic.Bool
}

// New starts unready. A nil checker means no dependency check.
func New(checker Checker, count func() int, metrics bool) *Handler {
	return &Handler{checker: checker, count: count, metrics: metrics}
}

// SetReady marks startup complete or shutdown in progress.
func (h *Handler) SetReady(ready bool) { h.ready.Store(ready) }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch r.URL.Path {
	case "/healthz", "/readyz":
	case "/metrics":
		if !h.metrics {
			http.NotFound(w, r)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/readyz" && (!h.ready.Load() || (h.checker != nil && h.checker.Check(r.Context()) != nil)) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/metrics" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if h.count != nil {
			fmt.Fprintf(w, "# HELP lwm2m_registrations Current registrations in the store.\n# TYPE lwm2m_registrations gauge\nlwm2m_registrations %d\n", h.count())
		}
		return
	}
	w.Write([]byte("ok\n"))
}
