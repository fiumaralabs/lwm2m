package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestDeviceCreatePutProfile(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", DeviceProfile{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, DeviceProfiles: profiles})
	body := `{"endpoint":"pump","profile":"p1"}`
	first := deviceRequest(h, "PUT", "/v1/device-creates/profile", body)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	replay := deviceRequest(h, "PUT", "/v1/device-creates/profile", body)
	if replay.Code != 200 || first.Body.String() != replay.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	mismatch := deviceRequest(h, "PUT", "/v1/device-creates/profile", `{"endpoint":"pump","profile":"other"}`)
	if mismatch.Code != 409 {
		t.Fatalf("mismatch: %d %s", mismatch.Code, mismatch.Body)
	}
	get := deviceRequest(h, "GET", "/v1/devices/pump", "")
	var got map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil || got["profile"] != "p1" {
		t.Fatalf("device: %d %s %v", get.Code, get.Body, err)
	}
	unknown := deviceRequest(h, "PUT", "/v1/device-creates/unknown", `{"endpoint":"other","profile":"missing"}`)
	if unknown.Code != 400 {
		t.Fatalf("unknown profile: %d %s", unknown.Code, unknown.Body)
	}
}
