package apiv1_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceMetadataPatchDurableContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles := apiv1.NewMemoryDeviceProfileStore()
	_ = profiles.Create("default", apiv1.DeviceProfile{ID: "p1"})
	hash := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%s devices:read,devices:write\nkey_read sha256:%s devices:read\n", hash("lwm_test_secret"), hash("lwm_read_secret"))))
	if err != nil {
		t.Fatal(err)
	}

	h := apiv1.New(apiv1.Options{Keys: keys, Devices: db, DeviceProfiles: profiles})
	req := func(method, path, body string) *httptest.ResponseRecorder {
		return limitRequest(h, method, path, body, "lwm_test_secret")
	}
	if w := req("POST", "/v1/devices", `{"endpoint":"one","name":"old","labels":{"old":"yes"}}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct{ body, name, labels, profile string }{
		{`{"name":"renamed"}`, "renamed", `"old":"yes"`, ""},
		{`{"labels":{"site":"north"}}`, "renamed", `"site":"north"`, ""},
		{`{"name":"pump","labels":{"env":"prod"},"profile":"p1"}`, "pump", `"env":"prod"`, "p1"},
	} {
		w := req("PATCH", "/v1/devices/one", tc.body)
		var d struct {
			Name, Profile string
			Labels        map[string]string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		labels, _ := json.Marshal(d.Labels)
		if w.Code != 200 || d.Name != tc.name || d.Profile != tc.profile || !strings.Contains(string(labels), tc.labels) || len(d.Labels) != 1 {
			t.Fatalf("patch %s: %d %s", tc.body, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"name":9}`, `{"labels":[]}`, `{"labels":{"bad key":"x"}}`, `{"labels":{"site":null}}`, `{"labels":{"site":4}}`, `{"labels":{"site":"bad value"}}`} {
		w := req("PATCH", "/v1/devices/one", body)
		if w.Code != 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
			t.Errorf("invalid %s: %d %s", body, w.Code, w.Body)
		}
	}
	for _, tc := range []struct {
		token string
		code  int
	}{{"lwm_read_secret", 403}} {
		w := limitRequest(h, "PATCH", "/v1/devices/one", `{"name":"no"}`, tc.token)
		if w.Code != tc.code {
			t.Errorf("auth: %d %s", w.Code, w.Body)
		}
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
	for _, target := range []string{"/v1/devices/one", "/v1/devices?q=pump", "/v1/devices?selector=env%3Dprod"} {
		w := req("GET", target, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"pump"`) || !strings.Contains(w.Body.String(), `"env":"prod"`) {
			t.Errorf("restart GET %s: %d %s", target, w.Code, w.Body)
		}
	}
	w := req("PATCH", "/v1/devices/one", `{"name":null,"labels":null,"profile":null}`)
	var d struct {
		Name, Profile string
		Labels        map[string]string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || d.Name != "" || d.Profile != "" || len(d.Labels) != 0 || d.Labels == nil {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
}
