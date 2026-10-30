package apiv1

import (
	"encoding/json"
	"testing"
)

func TestDeviceProfileReadContract(t *testing.T) {
	profiles := NewMemoryDeviceProfileStore()
	for _, p := range []DeviceProfile{{ID: "dp_a", Name: "pump"}, {ID: "dp_b", Name: "sensor"}} {
		if err := profiles.Create("default", p); err != nil {
			t.Fatal(err)
		}
	}
	if err := profiles.Create("other", DeviceProfile{ID: "dp_foreign", Name: "private"}); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), DeviceProfiles: profiles})
	w := deviceRequest(h, "GET", "/v1/device-profiles?limit=1", "")
	var page struct {
		Items      []DeviceProfile `json:"items"`
		NextCursor string          `json:"nextCursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != "dp_a" || page.NextCursor == "" {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/device-profiles?limit=1&cursor="+page.NextCursor, "")
	page = struct {
		Items      []DeviceProfile `json:"items"`
		NextCursor string          `json:"nextCursor"`
	}{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != "dp_b" || page.NextCursor != "" {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}
	if w := deviceRequest(New(Options{Keys: deviceKeys()}), "GET", "/v1/device-profiles", ""); w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
		t.Fatalf("default empty store: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		if w := deviceRequest(h, method, "/v1/device-profiles/dp_a", ""); w.Code != 405 {
			t.Fatalf("%s unexpectedly available: %d", method, w.Code)
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/v1/device-profiles/dp_a", 200}, {"/v1/device-profiles/dp_foreign", 404}, {"/v1/device-profiles/dp_unknown", 404}, {"/v1/device-profiles?limit=0", 400}, {"/v1/device-profiles?cursor=invalid", 400}} {
		w = deviceRequest(h, "GET", tc.path, "")
		if w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
