package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestDeviceCreateProfileContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", DeviceProfile{ID: "p1", Name: "Pump"}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Create("other", DeviceProfile{ID: "private", Name: "Private"}); err != nil {
		t.Fatal(err)
	}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, DeviceProfiles: profiles})
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"endpoint":"one","profile":"p1"}`, 201},
		{`{"endpoint":"two","profile":"unknown"}`, 400},
		{`{"endpoint":"three","profile":"private"}`, 400},
	} {
		w := deviceRequest(h, "POST", "/v1/devices", tc.body)
		if w.Code != tc.status {
			t.Errorf("POST %s: got %d %s, want %d", tc.body, w.Code, w.Body.String(), tc.status)
		}
	}
	w := deviceRequest(h, "GET", "/v1/devices/one", "")
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got["profile"] != "p1" {
		t.Errorf("GET got %d %s", w.Code, w.Body.String())
	}
}

func TestDevicePatchProfileContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	_ = profiles.Create("default", DeviceProfile{ID: "p1"})
	_ = profiles.Create("other", DeviceProfile{ID: "private"})
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, DeviceProfiles: profiles})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"one"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		body    string
		status  int
		profile string
	}{
		{`{"profile":"p1"}`, 200, "p1"}, {`{"profile":"private"}`, 400, "p1"}, {`{"profile":"unknown"}`, 400, "p1"}, {`{"profile":null}`, 200, ""},
	} {
		w := deviceRequest(h, "PATCH", "/v1/devices/one", tc.body)
		if w.Code != tc.status {
			t.Errorf("PATCH %s: %d %s", tc.body, w.Code, w.Body.String())
		}
		got := deviceRequest(h, "GET", "/v1/devices/one", "")
		var value map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if value["profile"] != tc.profile {
			t.Errorf("after %s got %s", tc.body, got.Body.String())
		}
	}
}
