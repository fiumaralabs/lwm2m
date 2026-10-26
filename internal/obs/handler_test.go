package obs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/internal/obs"
)

type checkFunc func(context.Context) error

func (f checkFunc) Check(ctx context.Context) error { return f(ctx) }

func probe(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestReadiness(t *testing.T) {
	for _, dependency := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-checker", true: "checker"}[dependency], func(t *testing.T) {
			var checker obs.Checker
			var failed bool
			calls := 0
			if dependency {
				checker = checkFunc(func(ctx context.Context) error {
					calls++
					if ctx == nil {
						t.Fatal("missing request context")
					}
					if failed {
						return errors.New("private dependency detail")
					}
					return nil
				})
			}
			h := obs.New(checker, nil, false)
			if w := probe(h, "GET", "/readyz"); w.Code != 503 {
				t.Fatalf("startup: %d", w.Code)
			}
			if calls != 0 {
				t.Fatal("checked dependency before startup")
			}
			h.SetReady(true)
			if w := probe(h, "GET", "/readyz"); w.Code != 200 {
				t.Fatalf("ready: %d", w.Code)
			}
			failed = true
			if dependency {
				if w := probe(h, "GET", "/readyz"); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("dependency: %d %q", w.Code, w.Body.String())
				}
				if w := probe(h, "GET", "/healthz"); w.Code != 200 {
					t.Fatal("dependency affected liveness")
				}
			}
			h.SetReady(false)
			if w := probe(h, "GET", "/readyz"); w.Code != 503 {
				t.Fatalf("shutdown: %d", w.Code)
			}
		})
	}
}

func TestRouting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := obs.New(nil, nil, enabled)
		for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
			for _, method := range []string{"POST", "HEAD", "PUT", "DELETE"} {
				w := probe(h, method, path)
				want := 405
				if path == "/metrics" && !enabled {
					want = 404
				}
				if w.Code != want || (want == 405 && w.Header().Get("Allow") != "GET") {
					t.Errorf("%v %s %s: %d %v", enabled, method, path, w.Code, w.Header())
				}
			}
		}
		for _, path := range []string{"/unknown", "/healthz/", "/readyz/child", "/metrics/child"} {
			if w := probe(h, "GET", path); w.Code != 404 {
				t.Errorf("%s: %d", path, w.Code)
			}
		}
		if !enabled {
			if w := probe(h, "GET", "/metrics"); w.Code != 404 {
				t.Errorf("disabled: %d", w.Code)
			}
		}
	}
}

func TestHealth(t *testing.T) {
	h := obs.New(nil, nil, false)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "ok" {
		t.Fatalf("health: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("content type: %q", got)
	}
}
