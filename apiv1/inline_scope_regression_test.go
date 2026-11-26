package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestInlineCredentialsRequireCredentialScope(t *testing.T) {
	devices := &memory.Devices{}
	s := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{
		"limited": {hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",devices:write,", tenant: "default"},
		"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"},
	}}
	h := New(Options{Keys: keys, Devices: devices, Server: s})
	call := func(token, endpoint string) *httptest.ResponseRecorder {
		body := `{"endpoint":"` + endpoint + `","credentials":{"psk":{"identity":"` + endpoint + `","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	denied := call("lwm_limited_secret", "denied")
	if denied.Code != 403 {
		t.Fatalf("denied: %d %s", denied.Code, denied.Body)
	}
	if _, err := devices.Get("default", "denied"); err == nil {
		t.Fatal("denied request created device")
	}
	if _, ok := s.Security().ByEndpoint("denied"); ok {
		t.Fatal("denied request installed credential")
	}
	allowed := call("lwm_allowed_secret", "allowed")
	if allowed.Code != 201 {
		t.Fatalf("allowed: %d %s", allowed.Code, allowed.Body)
	}
	if _, ok := s.Security().ByEndpoint("allowed"); !ok {
		t.Fatal("allowed request did not install credential")
	}
}

func TestCredentialWriteScopeStandalone(t *testing.T) {
	s := server.New(server.Config{})
	t.Cleanup(func() { _ = s.Close() })
	devices := &memory.Devices{}
	keys := Keys{entries: map[string]credential{
		"limited": {hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",devices:write,", tenant: "default"},
		"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",credentials:write,", tenant: "default"},
	}}
	h := New(Options{Keys: keys, Devices: devices, Server: s})
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/v1/devices", "lwm_limited_secret", `{"endpoint":"plain"}`); w.Code != 201 {
		t.Fatalf("plain create: %d %s", w.Code, w.Body)
	}
	body := `{"psk":{"identity":"plain","key":"hex:000102030405060708090a0b0c0d0e0f"}}`
	if w := call("PUT", "/v1/devices/plain/credentials", "lwm_limited_secret", body); w.Code != 403 {
		t.Fatalf("limited PUT: %d %s", w.Code, w.Body)
	}
	if _, ok := s.Security().ByEndpoint("plain"); ok {
		t.Fatal("denied PUT installed credentials")
	}
	if w := call("PUT", "/v1/devices/plain/credentials", "lwm_allowed_secret", body); w.Code != 201 {
		t.Fatalf("allowed PUT: %d %s", w.Code, w.Body)
	}
	if w := call("DELETE", "/v1/devices/plain/credentials", "lwm_limited_secret", ""); w.Code != 403 {
		t.Fatalf("limited DELETE: %d %s", w.Code, w.Body)
	}
	if _, ok := s.Security().ByEndpoint("plain"); !ok {
		t.Fatal("denied DELETE removed credentials")
	}
	if w := call("DELETE", "/v1/devices/plain/credentials", "lwm_allowed_secret", ""); w.Code != 204 {
		t.Fatalf("allowed DELETE: %d %s", w.Code, w.Body)
	}
}
