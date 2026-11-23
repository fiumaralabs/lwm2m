package apiv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
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
			if method == "parameters" {
				continue
			}
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

// Every operation must have a meaningful resource grouping for OpenAPI consumers.
func TestOpenAPIOperationTags(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if method == "parameters" {
				continue
			}
			var operation struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			if len(operation.Tags) == 0 {
				t.Errorf("%s %s has no resource tag", method, path)
			}
			for _, tag := range operation.Tags {
				if strings.TrimSpace(tag) == "" {
					t.Errorf("%s %s has empty resource tag", method, path)
				}
			}
		}
	}
}

func TestOpenAPIDeviceCreateRoutesDocumented(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"get", "put", "delete"} {
		if _, ok := doc.Paths["/v1/device-creates/{id}"][method]; !ok {
			t.Errorf("missing %s /v1/device-creates/{id}", method)
		}
	}
}

func TestOpenAPIInventoryRoutes(t *testing.T) {
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x admin,platform", sha256.Sum256([]byte("lwm_test_secret")))))
	if err != nil {
		t.Fatal(err)
	}
	configs := NewMemoryESTConfigStore()
	configs.Put("default", ESTConfig{})
	for _, route := range openAPIRoutes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			devices := &memory.Devices{}
			_, err := devices.Create("default", store.Device{Endpoint: "probe"})
			if err != nil {
				t.Fatal(err)
			}
			profiles := NewMemoryDeviceProfileStore()
			if err := profiles.Create("default", DeviceProfile{ID: "probe", Name: "probe"}); err != nil {
				t.Fatal(err)
			}
			jobs := NewMemoryJobStore()
			if err := jobs.Create("default", Job{ID: "probe", Target: JobTarget{Devices: []string{"probe"}}, Action: JobAction{Operation: json.RawMessage(`{}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			firmware := NewMemoryFirmwareStore()
			if err := firmware.Create("default", Firmware{ID: "probe"}); err != nil {
				t.Fatal(err)
			}
			updates := NewMemoryFirmwareUpdateStore()
			if err := updates.Create("default", FirmwareUpdate{ID: "fwu_probe", Device: "probe", URI: "https://example.test/fw"}); err != nil {
				t.Fatal(err)
			}
			anchors := NewMemoryTrustAnchorStore(0)
			if err := anchors.Create("default", TrustAnchor{ID: "probe"}); err != nil {
				t.Fatal(err)
			}
			webhooks := &memory.Webhooks{}
			if _, err := webhooks.Create("default", store.Webhook{ID: "probe"}, ""); err != nil {
				t.Fatal(err)
			}
			observations := &memory.Observations{}
			if err := observations.Create("default", store.Observation{Tenant: "default", ID: "probe", Device: "probe"}); err != nil {
				t.Fatal(err)
			}
			ops := memory.NewOperations()
			if err := ops.Create(context.Background(), "default", operations.Operation{Tenant: "default", ID: "probe", Device: "probe", Status: operations.Queued}); err != nil {
				t.Fatal(err)
			}
			models := NewMemoryModelStore()
			models.Put("default", &model.Object{ID: 50000, Version: model.Version{Major: 1}})
			keys.state = newKeyStore(keys.entries)
			if err := keys.state.Create(store.APIKey{KeyMetadata: store.KeyMetadata{ID: "probe"}, Tenant: "default"}); err != nil {
				t.Fatal(err)
			}
			h := New(Options{Keys: keys, ESTConfigs: configs, Devices: devices, DeviceProfiles: profiles, Jobs: jobs, Firmware: firmware, FirmwareUpdates: updates, TrustAnchors: anchors, Webhooks: webhooks, Observations: observations, Operations: ops, Models: models, TimeSeries: NewMemoryTimeSeriesStore(), Gateway: &endDeviceFixture{}})
			r := httptest.NewRequest(route.Method, route.Example, nil)
			if route.Path == "/v1/events" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			r.Header.Set("Authorization", "Bearer lwm_test_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == http.StatusNotFound {
				var problem Problem
				if json.Unmarshal(w.Body.Bytes(), &problem) != nil {
					t.Fatalf("invalid 404: %s", w.Body.String())
				}
				allowed := map[string]string{
					"DELETE /v1/device-creates/{id}":                "Outcome not found",
					"GET /v1/devices/{ep}/registration":             "Device is not registered",
					"DELETE /v1/devices/{ep}/registration":          "Device is not registered",
					"GET /v1/devices/{ep}/model":                    "Device is not registered",
					"GET /v1/devices/{ep}/credentials":              "Device not found",
					"PUT /v1/devices/{ep}/credentials":              "Device not found",
					"DELETE /v1/devices/{ep}/credentials":           "Device not found",
					"GET /v1/devices/{ep}/bootstrap":                "Bootstrap config not found",
					"DELETE /v1/devices/{ep}/bootstrap":             "Bootstrap config not found",
					"GET /v1/devices/{ep}/bootstrap/credentials":    "Credential not found",
					"DELETE /v1/devices/{ep}/bootstrap/credentials": "Credential not found",
				}
				if detail, ok := allowed[route.Method+" "+route.Path]; !ok || problem.Detail != detail {
					t.Fatalf("unknown path or missing fixture: %s", w.Body.String())
				}
			} else if w.Code == http.StatusForbidden && route.Method == "DELETE" && route.Path == "/v1/tenants/{id}" && strings.Contains(w.Body.String(), "Cannot delete default tenant") {
				// The built-in tenant cannot be removed.
			} else if w.Code == http.StatusServiceUnavailable && (route.Path == "/v1/devices/{ep}/operations" || route.Path == "/v1/operations/{id}") && strings.Contains(w.Body.String(), "No executor for tenant") {
				// The route is active, but this isolated handler has no device executor.
			} else if w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code >= 500 {
				t.Fatalf("unrouted, unauthorized or server error: status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/openapi.json", nil))
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unrouted control: status %d", w.Code)
	}
}
