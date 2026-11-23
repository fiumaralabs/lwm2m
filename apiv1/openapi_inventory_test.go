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

// TestOpenAPIInventory checks the published operations against the route inventory.
func TestOpenAPIInventory(t *testing.T) {
	if len(openAPIRoutes) < 20 {
		t.Fatalf("route inventory has only %d entries", len(openAPIRoutes))
	}
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for _, route := range openAPIRoutes {
		key := route.Method + " " + route.Path
		if want[key] {
			t.Errorf("duplicate inventory entry %s", key)
		}
		want[key] = true
	}
	for path, methods := range doc.Paths {
		for method := range methods {
			key := strings.ToUpper(method) + " " + path
			if !want[key] {
				t.Errorf("documented operation missing from inventory: %s", key)
			}
			delete(want, key)
		}
	}
	for key := range want {
		t.Errorf("inventory operation missing from document: %s", key)
	}
}

func TestOpenAPIInventoryRoutes(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x admin", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	configs := NewMemoryESTConfigStore()
	configs.Put("default", ESTConfig{})
	h := New(Options{Keys: keys, ESTConfigs: configs})
	for _, route := range openAPIRoutes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			r := httptest.NewRequest(route.Method, route.Path, nil)
			r.Header.Set("Authorization", "Bearer lwm_test_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
				t.Fatalf("unrouted or unauthorized: status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/openapi.json", nil))
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unrouted control: status %d", w.Code)
	}
}
