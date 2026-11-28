package apiv1_test

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceCreatePutProfileSQLiteRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles := apiv1.NewMemoryDeviceProfileStore()
	if err := profiles.Create("default", apiv1.DeviceProfile{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\n", hash)))
	if err != nil {
		t.Fatal(err)
	}
	h := apiv1.New(apiv1.Options{Keys: keys, Devices: db, DeviceProfiles: profiles})
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"endpoint":"pump","profile":"p1"}`
	first := call("/v1/device-creates/profile", body)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h = apiv1.New(apiv1.Options{Keys: keys, Devices: db, DeviceProfiles: profiles})
	replay := call("/v1/device-creates/profile", body)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay after restart: %d %s", replay.Code, replay.Body)
	}
	mismatch := call("/v1/device-creates/profile", `{"endpoint":"pump","profile":"other"}`)
	if mismatch.Code != 409 {
		t.Fatalf("mismatch: %d %s", mismatch.Code, mismatch.Body)
	}
	d, err := db.Get("default", "pump")
	if err != nil || d.Profile != "p1" {
		t.Fatalf("persisted device: %+v: %v", d, err)
	}
	unknown := call("/v1/device-creates/unknown", `{"endpoint":"other","profile":"missing"}`)
	if unknown.Code != 400 {
		t.Fatalf("unknown profile: %d %s", unknown.Code, unknown.Body)
	}
}
