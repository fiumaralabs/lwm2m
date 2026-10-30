package apiv1

import (
	"crypto/sha256"
	"testing"
)

func TestDeviceProfileWriteOnlyScopeRouting(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", DeviceProfile{ID: "dp_a", Name: "pump"}); err != nil {
		t.Fatal(err)
	}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, DeviceProfiles: profiles})
	if w := deviceRequest(h, "GET", "/v1/device-profiles/dp_a", ""); w.Code != 403 {
		t.Fatalf("read without read scope: %d", w.Code)
	}
	if w := deviceRequest(h, "DELETE", "/v1/device-profiles/dp_a", ""); w.Code != 204 {
		t.Fatalf("write-only delete: %d %s", w.Code, w.Body.String())
	}
}

func TestDeviceProfileReadOnlyScopeRouting(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := "/v1/device-profiles"
		if method != "POST" {
			path += "/dp_a"
		}
		if w := deviceRequest(h, method, path, ""); w.Code != 403 {
			t.Errorf("%s: %d", method, w.Code)
		}
	}
}
