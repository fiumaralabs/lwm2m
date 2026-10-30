package apiv1

import (
	"encoding/json"
	"strings"
	"testing"
)

type reverseProfileStore struct{ DeviceProfileStore }

func (s reverseProfileStore) List(tenant string) []DeviceProfile {
	profiles := s.DeviceProfileStore.List(tenant)
	for i, j := 0, len(profiles)-1; i < j; i, j = i+1, j-1 {
		profiles[i], profiles[j] = profiles[j], profiles[i]
	}
	return profiles
}

func TestDeviceProfileGetETag(t *testing.T) {
	store := NewMemoryDeviceProfileStore()
	profile := DeviceProfile{ID: "dp_a", Name: "pump"}
	if err := store.Create("default", profile); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), DeviceProfiles: store})
	get := func() string {
		w := deviceRequest(h, "GET", "/v1/device-profiles/dp_a", "")
		tag := w.Header().Get("ETag")
		if w.Code != 200 || !strings.HasPrefix(tag, `"profile-v1-`) || !strings.HasSuffix(tag, `"`) {
			t.Fatalf("invalid ETag for status %d: %q", w.Code, tag)
		}
		return tag
	}
	first := get()
	if got := get(); got != first {
		t.Fatalf("ETag changed without mutation: %q != %q", got, first)
	}
	profile.Name = "sensor"
	if !store.Replace("default", profile) {
		t.Fatal("replace failed")
	}
	if got := get(); got == first {
		t.Fatal("ETag did not change with profile")
	}
	missing := deviceRequest(h, "GET", "/v1/device-profiles/missing", "")
	if missing.Code != 404 || missing.Header().Get("ETag") != "" {
		t.Fatalf("missing: %d, %q", missing.Code, missing.Header().Get("ETag"))
	}
}

func TestDeviceProfilePaginationUnorderedStore(t *testing.T) {
	store := NewMemoryDeviceProfileStore()
	for _, id := range []string{"dp_a", "dp_b"} {
		if err := store.Create("default", DeviceProfile{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	h := New(Options{Keys: deviceKeys(), DeviceProfiles: reverseProfileStore{store}})
	cursor := ""
	for _, want := range []string{"dp_a", "dp_b"} {
		path := "/v1/device-profiles?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := deviceRequest(h, "GET", path, "")
		var page struct {
			Items []DeviceProfile `json:"items"`
			Next  string          `json:"nextCursor"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != want {
			t.Fatalf("want %s: %d %s", want, w.Code, w.Body.String())
		}
		cursor = page.Next
		if want == "dp_a" && cursor == "" {
			t.Fatal("missing continuation")
		}
		if want == "dp_b" && cursor != "" {
			t.Fatal("unexpected continuation")
		}
	}
}
