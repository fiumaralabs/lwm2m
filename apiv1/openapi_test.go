package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIUnauthenticated(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x admin", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys})
	secured := httptest.NewRecorder()
	h.ServeHTTP(secured, httptest.NewRequest(http.MethodGet, "/v1/info", nil))
	if secured.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control: status %d, want 401", secured.Code)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/openapi.json: status %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type %q", got)
	}
	var doc struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version %q", doc.OpenAPI)
	}
	if _, ok := doc.Paths["/v1/openapi.json"]; !ok {
		t.Fatal("OpenAPI endpoint is not documented")
	}
}

func TestOpenAPIMethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/openapi.json", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != "GET" {
		t.Fatalf("Allow: %q, want GET", got)
	}
}
