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
	s := server.New(server.Config{})
	t.Cleanup(func() { _ = s.Close() })
	devices := &memory.Devices{}
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
