package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"testing"
)

func TestDeviceProfileDeleteContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", DeviceProfile{ID: "dp_owned", Name: "pump"}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Create("other", DeviceProfile{ID: "dp_foreign", Name: "private"}); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), DeviceProfiles: profiles})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"DELETE", "/v1/device-profiles/dp_foreign", 404},
		{"DELETE", "/v1/device-profiles/dp_unknown", 404},
		{"DELETE", "/v1/device-profiles/dp_owned", 204},
		{"GET", "/v1/device-profiles/dp_owned", 404},
	} {
		w := deviceRequest(h, tc.method, tc.path, "")
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if _, ok := profiles.Get("other", "dp_foreign"); !ok {
		t.Fatal("foreign profile deleted")
	}
}
func TestDeviceProfileDeleteAuthorization(t *testing.T) {
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,", tenant: "default"}}}
	h := New(Options{Keys: keys})
	r := httptest.NewRequest("DELETE", "/v1/device-profiles/dp_a", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("missing key: %d", w.Code)
	}
	if w = deviceRequest(h, "DELETE", "/v1/device-profiles/dp_a", ""); w.Code != 403 {
		t.Fatalf("read only: %d", w.Code)
	}
}
