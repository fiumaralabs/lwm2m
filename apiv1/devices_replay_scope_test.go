package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateReplayCredentialScope(t *testing.T) {
	devices := &memory.Devices{}
	s := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{
		"limited": {hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",devices:write,", tenant: "default"},
		"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"},
	}}
	h := New(Options{Keys: keys, Devices: devices, Server: s})
	post := func(token, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	cred := `{"endpoint":"secure","credentials":{"psk":{"identity":"secure","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	plain := `{"endpoint":"plain"}`
	first := post("lwm_allowed_secret", "secure-key", cred)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	for _, body := range []string{cred, plain} {
		denied := post("lwm_limited_secret", "secure-key", body)
		if denied.Code != 403 || !strings.Contains(denied.Body.String(), "insufficient_scope") {
			t.Errorf("secure replay: %d %s", denied.Code, denied.Body)
		}
	}
	replay := post("lwm_allowed_secret", "secure-key", plain)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Errorf("authorized replay: %d %s", replay.Code, replay.Body)
	}
	p := post("lwm_limited_secret", "plain-key", plain)
	if p.Code != 201 {
		t.Fatalf("plain: %d %s", p.Code, p.Body)
	}
	denied := post("lwm_limited_secret", "plain-key", cred)
	if denied.Code != 403 {
		t.Errorf("credential-bearing replay: %d %s", denied.Code, denied.Body)
	}
	replay = post("lwm_limited_secret", "plain-key", plain)
	if replay.Code != p.Code || replay.Body.String() != p.Body.String() {
		t.Errorf("plain replay: %d %s", replay.Code, replay.Body)
	}
	if _, err := devices.Get("default", "plain"); err != nil {
		t.Error("plain device missing")
	}
}
