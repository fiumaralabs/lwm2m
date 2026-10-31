package apiv1

import (
	"net/http/httptest"
	"testing"

	"github.com/fiumaralabs/lwm2m/gateway"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestRouterEndDevices(t *testing.T) {
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "gw"}); err != nil {
		t.Fatal(err)
	}
	source := &endDeviceFixture{links: mustLinks(t, `</25/0>,</d01/3303/0>`), devices: []gateway.Device{{Instance: 0, ID: "sensor", Prefix: "d01"}}}
	keys := deviceKeys()
	h := New(Options{Keys: keys, Devices: devices, Gateway: source})
	path := "/v1/devices/gw/end-devices"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	w = deviceRequest(h, "GET", path, "")
	if w.Code != 200 || w.Body.String() != "{\"items\":[{\"prefix\":\"d01\",\"id\":\"sensor\",\"instance\":0,\"objects\":[{\"id\":3303,\"instances\":[0]}]}]}\n" {
		t.Fatalf("listing: %d %s", w.Code, w.Body.String())
	}
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	w = deviceRequest(h, "GET", path, "")
	if w.Code != 404 || source.calls != 1 {
		t.Fatalf("tenant: %d calls=%d", w.Code, source.calls)
	}
	entry.tenant = "default"
	entry.scopes = ",devices:write,"
	keys.entries["test"] = entry
	w = deviceRequest(h, "GET", path, "")
	if w.Code != 403 {
		t.Fatalf("scope: %d", w.Code)
	}
}
