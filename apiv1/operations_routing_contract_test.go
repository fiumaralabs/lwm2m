package apiv1

import (
	"encoding/json"
	"testing"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
)

func TestOperationsNamedDeviceResource(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{
		{"GET", 200},
		{"PATCH", 405}, // Device PATCH is not implemented; preserve the device handler's response.
		{"DELETE", 204},
	} {
		t.Run(tc.method, func(t *testing.T) {
			// Device management must not require operations:write.
			h := New(Options{Keys: deviceKeys()})
			w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"operations"}`)
			if w.Code != 201 || w.Header().Get("Location") != "/v1/devices/operations" {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			w = deviceRequest(h, tc.method, "/v1/devices/operations", "")
			if w.Code != tc.status {
				t.Fatalf("%s: %d %s", tc.method, w.Code, w.Body.String())
			}
			switch tc.method {
			case "GET":
				var device deviceView
				if err := json.Unmarshal(w.Body.Bytes(), &device); err != nil || device.Endpoint != "operations" {
					t.Fatalf("device: %s", w.Body.String())
				}
			case "PATCH":
				if got := w.Header().Get("Allow"); got != "GET, DELETE" {
					t.Fatalf("Allow = %q, want device methods", got)
				}
			case "DELETE":
				if w := deviceRequest(h, "GET", "/v1/devices/operations", ""); w.Code != 404 {
					t.Fatalf("deleted device: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestOperationsNamedDeviceSubresource(t *testing.T) {
	keys := deviceKeys()
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: keys, Server: srv})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"operations"}`); w.Code != 201 {
		t.Fatalf("create device: %d %s", w.Code, w.Body.String())
	}
	const path = "/v1/devices/operations/operations"
	const body = `{"type":"read","paths":["/3/0/0"]}`
	if w := deviceRequest(h, "POST", path, body); w.Code != 403 {
		t.Fatalf("missing operations scope: %d %s", w.Code, w.Body.String())
	}
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	w := deviceRequest(h, "POST", path, body)
	if w.Code != 202 {
		t.Fatalf("create operation: %d %s", w.Code, w.Body.String())
	}
	var op operations.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || op.Device != "operations" || op.Status != operations.Queued {
		t.Fatalf("operation: %s", w.Body.String())
	}
	if w := deviceRequest(h, "GET", w.Header().Get("Location"), ""); w.Code != 200 {
		t.Fatalf("get operation: %d %s", w.Code, w.Body.String())
	}
}
