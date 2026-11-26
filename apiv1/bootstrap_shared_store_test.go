package apiv1

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestBootstrapSeamSharedWithAPI(t *testing.T) {
	for name, devices := range map[string]store.Devices{"memory": &memory.Devices{}} {
		t.Run(name, func(t *testing.T) {
			seam := devices.(store.DeviceBootstrapCreateStore)
			_, _, err := seam.CreateDeviceWithSecurityAndBootstrap(context.Background(), "default", "op", "fp", store.Device{Endpoint: "a"}, server.SecurityInfo{}, json.RawMessage(`{"servers":[{"shortServerId":1}]}`))
			if err != nil {
				t.Fatal(err)
			}
			keys := deviceKeys()
			c := keys.entries["test"]
			c.scopes += "bootstrap:write,"
			keys.entries["test"] = c
			h := New(Options{Devices: devices, Keys: keys})
			path := "/v1/devices/a/bootstrap"
			w := deviceRequest(h, "GET", path, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"shortServerId":1`) {
				t.Fatalf("GET %d %s", w.Code, w.Body.String())
			}
			w = deviceRequest(h, "PUT", path, `{}`)
			if w.Code != 204 {
				t.Fatalf("PUT %d %s", w.Code, w.Body.String())
			}
			b, ok, err := seam.GetBootstrapConfig(context.Background(), "default", "a")
			if err != nil || !ok || string(b) != `{}` {
				t.Fatalf("seam GET %q %v %v", b, ok, err)
			}
			w = deviceRequest(h, "DELETE", path, "")
			if w.Code != 204 {
				t.Fatalf("DELETE %d %s", w.Code, w.Body.String())
			}
			_, ok, err = seam.GetBootstrapConfig(context.Background(), "default", "a")
			if err != nil || ok {
				t.Fatalf("deleted: %v %v", ok, err)
			}
		})
	}
}
