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

func TestOpenAPIModelDocumented(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Paths["/v1/models/{id}"]["get"]; !ok {
		t.Fatal("model GET is not documented")
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

// Probes are an explicit sample of implemented method and path pairs.
var openAPIRoutes = []struct{ Method, Path, Example string }{
	{"GET", "/v1/openapi.json", "/v1/openapi.json"},
	{"GET", "/v1/info", "/v1/info"},
	{"GET", "/v1/events", "/v1/events"},
	{"GET", "/v1/devices", "/v1/devices"},
	{"POST", "/v1/devices", "/v1/devices"},
	{"GET", "/v1/device-creates/{id}", "/v1/device-creates/probe"},
	{"PUT", "/v1/device-creates/{id}", "/v1/device-creates/probe"},
	{"DELETE", "/v1/device-creates/{id}", "/v1/device-creates/probe"},
	{"GET", "/v1/device-profiles", "/v1/device-profiles"},
	{"POST", "/v1/device-profiles", "/v1/device-profiles"},
	{"GET", "/v1/jobs", "/v1/jobs"},
	{"POST", "/v1/jobs", "/v1/jobs"},
	{"GET", "/v1/firmware", "/v1/firmware"},
	{"POST", "/v1/firmware", "/v1/firmware"},
	{"GET", "/v1/trust-anchors", "/v1/trust-anchors"},
	{"POST", "/v1/trust-anchors", "/v1/trust-anchors"},
	{"GET", "/v1/est", "/v1/est"},
	{"PUT", "/v1/est", "/v1/est"},
	{"GET", "/v1/models", "/v1/models"},
	{"GET", "/v1/models/{id}", "/v1/models/3"},
	{"POST", "/v1/models", "/v1/models"},
	{"GET", "/v1/webhooks", "/v1/webhooks"},
	{"POST", "/v1/webhooks", "/v1/webhooks"},
	{"GET", "/v1/api-keys", "/v1/api-keys"},
	{"POST", "/v1/api-keys", "/v1/api-keys"},
	{"GET", "/v1/settings", "/v1/settings"},
	{"PATCH", "/v1/settings", "/v1/settings"},
	{"GET", "/v1/audit-log", "/v1/audit-log"},
	{"GET", "/v1/operations", "/v1/operations"},
	{"GET", "/v1/devices/{ep}", "/v1/devices/probe"},
	{"PATCH", "/v1/devices/{ep}", "/v1/devices/probe"},
	{"DELETE", "/v1/devices/{ep}", "/v1/devices/probe"},
	{"GET", "/v1/devices/{ep}/state", "/v1/devices/probe/state"},
	{"GET", "/v1/devices/{ep}/registration", "/v1/devices/probe/registration"},
	{"DELETE", "/v1/devices/{ep}/registration", "/v1/devices/probe/registration"},
	{"GET", "/v1/devices/{ep}/model", "/v1/devices/probe/model"},
	{"GET", "/v1/devices/{ep}/events", "/v1/devices/probe/events"},
	{"GET", "/v1/devices/{ep}/credentials", "/v1/devices/probe/credentials"},
	{"PUT", "/v1/devices/{ep}/credentials", "/v1/devices/probe/credentials"},
	{"DELETE", "/v1/devices/{ep}/credentials", "/v1/devices/probe/credentials"},
	{"GET", "/v1/devices/{ep}/bootstrap", "/v1/devices/probe/bootstrap"},
	{"PUT", "/v1/devices/{ep}/bootstrap", "/v1/devices/probe/bootstrap"},
	{"DELETE", "/v1/devices/{ep}/bootstrap", "/v1/devices/probe/bootstrap"},
	{"GET", "/v1/devices/{ep}/bootstrap/credentials", "/v1/devices/probe/bootstrap/credentials"},
	{"PUT", "/v1/devices/{ep}/bootstrap/credentials", "/v1/devices/probe/bootstrap/credentials"},
	{"DELETE", "/v1/devices/{ep}/bootstrap/credentials", "/v1/devices/probe/bootstrap/credentials"},
	{"GET", "/v1/devices/{ep}/bootstrap/sessions", "/v1/devices/probe/bootstrap/sessions"},
	{"GET", "/v1/devices/{ep}/operations", "/v1/devices/probe/operations"},
	{"POST", "/v1/devices/{ep}/operations", "/v1/devices/probe/operations"},
	{"GET", "/v1/devices/{ep}/observations", "/v1/devices/probe/observations"},
	{"POST", "/v1/devices/{ep}/observations", "/v1/devices/probe/observations"},
	{"GET", "/v1/devices/{ep}/timeseries", "/v1/devices/probe/timeseries"},
	{"GET", "/v1/devices/{ep}/firmware-updates", "/v1/devices/probe/firmware-updates"},
	{"POST", "/v1/devices/{ep}/firmware-updates", "/v1/devices/probe/firmware-updates"},
	{"GET", "/v1/observations/{id}", "/v1/observations/probe"},
	{"PATCH", "/v1/observations/{id}", "/v1/observations/probe"},
	{"DELETE", "/v1/observations/{id}", "/v1/observations/probe"},
	{"GET", "/v1/operations/{id}", "/v1/operations/probe"},
	{"DELETE", "/v1/operations/{id}", "/v1/operations/probe"},
	{"GET", "/v1/device-profiles/{id}", "/v1/device-profiles/probe"},
	{"PATCH", "/v1/device-profiles/{id}", "/v1/device-profiles/probe"},
	{"DELETE", "/v1/device-profiles/{id}", "/v1/device-profiles/probe"},
	{"GET", "/v1/jobs/{id}", "/v1/jobs/probe"},
	{"GET", "/v1/jobs/{id}/tasks", "/v1/jobs/probe/tasks"},
	{"POST", "/v1/jobs/{id}/pause", "/v1/jobs/probe/pause"},
	{"POST", "/v1/jobs/{id}/resume", "/v1/jobs/probe/resume"},
	{"POST", "/v1/jobs/{id}/cancel", "/v1/jobs/probe/cancel"},
	{"GET", "/v1/firmware/{id}", "/v1/firmware/probe"},
	{"GET", "/v1/firmware/{id}/content", "/v1/firmware/probe/content"},
	{"PUT", "/v1/firmware/{id}/content", "/v1/firmware/probe/content"},
	{"PATCH", "/v1/firmware/{id}", "/v1/firmware/probe"},
	{"DELETE", "/v1/firmware/{id}", "/v1/firmware/probe"},
	{"GET", "/v1/firmware-updates/{id}", "/v1/firmware-updates/fwu_probe"},
	{"DELETE", "/v1/firmware-updates/{id}", "/v1/firmware-updates/fwu_probe"},
	{"GET", "/v1/trust-anchors/{id}", "/v1/trust-anchors/probe"},
	{"DELETE", "/v1/trust-anchors/{id}", "/v1/trust-anchors/probe"},
	{"GET", "/v1/webhooks/{id}", "/v1/webhooks/probe"},
	{"PATCH", "/v1/webhooks/{id}", "/v1/webhooks/probe"},
	{"DELETE", "/v1/webhooks/{id}", "/v1/webhooks/probe"},
	{"POST", "/v1/webhooks/{id}/rotate-secret", "/v1/webhooks/probe/rotate-secret"},
	{"GET", "/v1/webhooks/{id}/deliveries", "/v1/webhooks/probe/deliveries"},
	{"POST", "/v1/webhooks/{id}/deliveries/{deliveryId}/retry", "/v1/webhooks/probe/deliveries/probe/retry"},
	{"POST", "/v1/webhooks/{id}/test", "/v1/webhooks/probe/test"},
	{"DELETE", "/v1/api-keys/{id}", "/v1/api-keys/probe"},
	{"DELETE", "/v1/models/{id}/versions/{version}", "/v1/models/50000/versions/1.0"},
	{"GET", "/v1/tenants", "/v1/tenants"},
	{"POST", "/v1/tenants", "/v1/tenants"},
	{"GET", "/v1/tenants/{id}", "/v1/tenants/default"},
	{"PATCH", "/v1/tenants/{id}", "/v1/tenants/default"},
	{"DELETE", "/v1/tenants/{id}", "/v1/tenants/default"},
	{"GET", "/v1/tenants/{id}/usage", "/v1/tenants/default/usage"},
	{"GET", "/v1/devices/{ep}/end-devices", "/v1/devices/probe/end-devices"},
}
