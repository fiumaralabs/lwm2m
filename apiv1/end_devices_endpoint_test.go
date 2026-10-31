package apiv1

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/gateway"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type endDeviceFixture struct {
	links   []link.Link
	devices []gateway.Device
	calls   int
}

func (f *endDeviceFixture) ReadGateway(_ context.Context, _, _ string) ([]link.Link, []gateway.Device, error) {
	f.calls++
	return f.links, f.devices, nil
}
func mustLinks(t *testing.T, s string) []link.Link {
	t.Helper()
	v, err := link.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestPrefixedRegistrationLinks(t *testing.T) {
	links := mustLinks(t, `</d01/3303>;ver="1.1",</d01/3303/0>,</d01/3303/1>,</d02/3/0>;ver="1.2",</d01/no>,</d01/3/0/1>,</3/0>`)
	got := prefixedObjects(links)
	want := map[string][]link.Object{"d01": {{ID: 3303, Version: "1.1", Instances: []uint16{0, 1}}}, "d02": {{ID: 3, Version: "1.2", Instances: []uint16{0}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func TestEndDevicesHandler(t *testing.T) {
	devices := &memory.Devices{}
	if _, err := devices.Create("default", store.Device{Endpoint: "gw"}); err != nil {
		t.Fatal(err)
	}
	source := &endDeviceFixture{links: mustLinks(t, `</25/0>,</25/1>,</d01/3303/0>;ver="1.1",</d02/3/0>`), devices: []gateway.Device{{Instance: 0, ID: "sensor", Prefix: "d01"}, {Instance: 1, ID: "meter", Prefix: "d02"}}}
	keys := deviceKeys()
	handler := Authenticate(keys, endDevicesHandler(devices, source))
	w := deviceRequest(handler, "GET", "/v1/devices/gw/end-devices", "")
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(`{"items":[{"prefix":"d01","id":"sensor","instance":0,"objects":[{"id":3303,"version":"1.1","instances":[0]}]},{"prefix":"d02","id":"meter","instance":1,"objects":[{"id":3,"instances":[0]}]}]}`), &want); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !reflect.DeepEqual(got, want) {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	source.links = mustLinks(t, `</3/0>,</d01/3/0>`)
	w = deviceRequest(handler, "GET", "/v1/devices/gw/end-devices", "")
	if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = deviceRequest(handler, "GET", "/v1/devices/missing/end-devices", "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	handler = Authenticate(keys, endDevicesHandler(devices, source))
	calls := source.calls
	w = deviceRequest(handler, "GET", "/v1/devices/gw/end-devices", "")
	if w.Code != 404 || source.calls != calls {
		t.Fatal(w.Code, source.calls, calls)
	}
	entry.tenant = "default"
	entry.scopes = ",devices:write,"
	keys.entries["test"] = entry
	handler = Authenticate(keys, endDevicesHandler(devices, source))
	w = deviceRequest(handler, "GET", "/v1/devices/gw/end-devices", "")
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
}
