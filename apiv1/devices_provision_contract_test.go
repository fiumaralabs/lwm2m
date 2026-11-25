package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

const provisionKey = "000102030405060708090a0b0c0d0e0f"
const provisionPSK = `{"psk":{"identity":"provision-id","key":"hex:` + provisionKey + `"}}`
const provisionBootstrap = `{"servers":[{"uri":"coaps://example.org:5684","shortServerId":1,"security":{"psk":{"identity":"bootstrap-id","key":"hex:` + provisionKey + `"}}}]}`

type provisionFixture struct {
	h       http.Handler
	devices *memory.Devices
	configs BootstrapConfigStore
	events  *memory.Events
	server  *server.Server
}

func newProvisionFixture(t *testing.T) *provisionFixture {
	t.Helper()
	s := server.New(server.Config{})
	t.Cleanup(func() { _ = s.Close() })
	d := &memory.Devices{}
	c := NewMemoryBootstrapConfigStore()
	e := &memory.Events{}
	keys := Keys{entries: map[string]credential{
		"test":    {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"},
		"foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "other"},
	}}
	return &provisionFixture{New(Options{Keys: keys, Devices: d, BootstrapConfigs: c, Server: s, EventOptions: EventOptions{Events: e}}), d, c, e, s}
}
func (f *provisionFixture) request(token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}
func (f *provisionFixture) artifacts(t *testing.T, tenant, ep string, device, credential, bootstrap bool) {
	t.Helper()
	_, err := f.devices.Get(tenant, ep)
	if (err == nil) != device {
		t.Errorf("device present=%v, want %v (err=%v)", err == nil, device, err)
	}
	_, ok := f.server.Security().ByEndpoint(ep)
	if ok != credential {
		t.Errorf("credential present=%v, want %v", ok, credential)
	}
	_, ok = f.configs.Get(tenant, ep)
	if ok != bootstrap {
		t.Errorf("bootstrap present=%v, want %v", ok, bootstrap)
	}
}
func TestDeviceProvisionContract(t *testing.T) {
	cases := []struct {
		name, fields       string
		status             int
		device, cred, boot bool
	}{
		{"legacy", "", 201, true, false, false},
		{"psk", `,"credentials":` + provisionPSK, 201, true, true, false},
		{"x509", `,"credentials":{"x509":{}}`, 201, true, false, false},
		{"bootstrap", `,"bootstrap":` + provisionBootstrap, 201, true, false, true},
		{"both", `,"credentials":` + provisionPSK + `,"bootstrap":` + provisionBootstrap, 201, true, true, true},
		{"invalid-credential", `,"credentials":{"psk":{"identity":"bad","key":"hex:00"}}`, 400, false, false, false},
		{"invalid-bootstrap", `,"credentials":` + provisionPSK + `,"bootstrap":{"servers":[{"shortServerId":1},{"shortServerId":1}]}`, 400, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProvisionFixture(t)
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			w := f.request("lwm_test_secret", `{"endpoint":"a"`+tc.fields+`}`)
			if w.Code != tc.status {
				t.Errorf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			f.artifacts(t, "default", "a", tc.device, tc.cred, tc.boot)
			if tc.name == "x509" && w.Code == 201 {
				c := deviceRequest(f.h, "GET", "/v1/devices/a/credentials", "")
				if c.Code != 200 || !strings.Contains(c.Body.String(), `"x509"`) {
					t.Errorf("x509 credential not persisted: %d %s", c.Code, c.Body.String())
				}
			}
			events, _, err := f.events.List("default", store.EventFilter{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(events)
			for _, secret := range []string{provisionKey, "hex:" + provisionKey} {
				if strings.Contains(w.Body.String(), secret) || strings.Contains(string(data), secret) || strings.Contains(logs.String(), secret) {
					t.Error("secret leaked in response, events, or logs")
				}
			}
		})
	}
}
func TestDeviceProvisionConflictPreservesArtifacts(t *testing.T) {
	f := newProvisionFixture(t)
	if w := f.request("lwm_test_secret", `{"endpoint":"a","name":"original"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(f.h, "PUT", "/v1/devices/a/credentials", provisionPSK); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := deviceRequest(f.h, "PUT", "/v1/devices/a/bootstrap", provisionBootstrap); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	before, _ := f.devices.Get("default", "a")
	cfg, _ := f.configs.Get("default", "a")
	sec, _ := f.server.Security().ByEndpoint("a")
	w := f.request("lwm_test_secret", `{"endpoint":"a","credentials":{"psk":{"identity":"new-id","key":"hex:`+provisionKey+`"}},"bootstrap":{}}`)
	if w.Code != 409 {
		t.Errorf("status=%d want 409", w.Code)
	}
	after, _ := f.devices.Get("default", "a")
	cfgAfter, _ := f.configs.Get("default", "a")
	secAfter, _ := f.server.Security().ByEndpoint("a")
	if before.Name != after.Name || !bytes.Equal(cfg, cfgAfter) || sec.PSKIdentity != secAfter.PSKIdentity || !bytes.Equal(sec.PSKKey, secAfter.PSKKey) {
		t.Error("conflict changed existing artifacts")
	}
	if _, ok := f.server.Security().ByPSKIdentity("new-id"); ok {
		t.Error("conflict created new credential")
	}
	f.artifacts(t, "default", "a", true, true, true)
}
func TestDeviceProvisionTenantIsolation(t *testing.T) {
	f := newProvisionFixture(t)
	w := f.request("lwm_test_secret", `{"endpoint":"a","credentials":`+provisionPSK+`,"bootstrap":`+provisionBootstrap+`}`)
	if w.Code != 201 {
		t.Errorf("status=%d want 201", w.Code)
	}
	f.artifacts(t, "default", "a", true, true, true)
	if _, err := f.devices.Get("other", "a"); err == nil {
		t.Error("foreign device created")
	}
	if _, ok := f.configs.Get("other", "a"); ok {
		t.Error("foreign bootstrap created")
	}
	r := httptest.NewRequest("GET", "/v1/devices/a/credentials", nil)
	r.Header.Set("Authorization", "Bearer lwm_foreign_secret")
	out := httptest.NewRecorder()
	f.h.ServeHTTP(out, r)
	if out.Code != 404 {
		t.Errorf("foreign credentials status=%d", out.Code)
	}
}
