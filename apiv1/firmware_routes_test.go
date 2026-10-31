package apiv1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestRouterFirmwareUpdates(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes = ",devices:read,firmware:write,"
	keys.entries["test"] = entry
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "dev"}); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Devices: devices})
	collection := "/v1/devices/dev/firmware-updates"
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, collection, nil))
	if unauth.Code != 401 {
		t.Fatalf("unauthenticated: %d", unauth.Code)
	}
	w := deviceRequest(h, "POST", collection, `{"uri":"coaps://example/fw"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var u FirmwareUpdate
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil || u.ID == "" || u.Status != "pending" {
		t.Fatalf("create body: %s %v", w.Body.String(), err)
	}
	path := "/v1/firmware-updates/" + u.ID
	for _, p := range []string{collection, path} {
		if w = deviceRequest(h, "GET", p, ""); w.Code != 200 {
			t.Fatalf("GET %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	entry.tenant = "other"
	keys.entries["test"] = entry
	for _, method := range []string{"GET", "POST"} {
		if w = deviceRequest(h, method, collection, `{"uri":"x"}`); w.Code != 404 {
			t.Fatalf("cross tenant collection %s: %d", method, w.Code)
		}
	}
	for _, method := range []string{"GET", "DELETE"} {
		if w = deviceRequest(h, method, path, ""); w.Code != 404 {
			t.Fatalf("cross tenant %s: %d", method, w.Code)
		}
	}
	entry.tenant = "default"
	entry.scopes = ",devices:write,"
	keys.entries["test"] = entry
	for _, tc := range []struct{ method, path string }{{"GET", collection}, {"POST", collection}, {"GET", path}, {"DELETE", path}} {
		if w = deviceRequest(h, tc.method, tc.path, `{"uri":"x"}`); w.Code != 403 {
			t.Fatalf("scope %s %s: %d", tc.method, tc.path, w.Code)
		}
	}
	entry.scopes = ",devices:read,firmware:write,"
	keys.entries["test"] = entry
	// Use the original handler for deletion, since it owns the in-memory update store.
	if w = deviceRequest(h, "DELETE", path, ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

func TestRouterFirmwareUpdatesEscapedEndpoint(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes = ",devices:read,firmware:write,"
	keys.entries["test"] = entry
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "pump/a"}); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Devices: devices})
	path := "/v1/devices/pump%2Fa/firmware-updates"
	w := deviceRequest(h, "POST", path, `{"uri":"coaps://example/fw"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var update FirmwareUpdate
	if err := json.Unmarshal(w.Body.Bytes(), &update); err != nil || update.Device != "pump/a" {
		t.Fatalf("create body: %s %v", w.Body.String(), err)
	}
	w = deviceRequest(h, "GET", path, "")
	var page struct {
		Items []FirmwareUpdate `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); w.Code != 200 || err != nil || len(page.Items) != 1 || page.Items[0].ID != update.ID || page.Items[0].Device != "pump/a" {
		t.Fatalf("list: %d %s %v", w.Code, w.Body.String(), err)
	}
	entry.tenant = "other"
	keys.entries["test"] = entry
	for _, method := range []string{"GET", "POST"} {
		w = deviceRequest(h, method, path, `{"uri":"coaps://example/fw"}`)
		if w.Code != 404 {
			t.Errorf("cross-tenant %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}
