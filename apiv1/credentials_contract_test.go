package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestCredentialsErrorsAndGeneration(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	keys := Keys{entries: map[string]credential{
		"test":    {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"},
		"foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,devices:write,", tenant: "other"},
		"read":    {hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "default"},
	}}
	events := &memory.Events{}
	h := New(Options{Keys: keys, Server: s, EventOptions: EventOptions{Events: events}})
	for _, ep := range []string{"a", "b"} {
		if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"`+ep+`"}`); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	for _, tc := range []struct {
		body, code string
		status     int
	}{
		{`{"psk":{"identity":"a","key":"hex:00"}}`, "invalid_credential", 400},
		{`{"psk":{"identity":"a","key":"bad"}}`, "invalid_credential", 400},
		{`{"rpk":{}}`, "rpk_unsupported", 501},
		{`{"x509":{}}`, "security_mode_unsupported", 501},
		{`{"oscore":{}}`, "security_mode_unsupported", 501},
	} {
		w := deviceRequest(h, "PUT", "/v1/devices/a/credentials", tc.body)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	w := deviceRequest(h, "PUT", "/v1/devices/a/credentials", `{"psk":{"generate":true}}`)
	var resp credentialResponse
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.PSK.Identity == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(resp.PSK.Key, "base64:"))
	if err != nil || len(key) != 32 {
		t.Fatal("bad generated key")
	}
	w = deviceRequest(h, "PUT", "/v1/devices/b/credentials", `{"psk":{"identity":"`+resp.PSK.Identity+`","key":"base64:AAECAwQFBgcICQoLDA0ODw=="}}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "duplicate_psk_identity") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/v1/devices/missing/credentials", "lwm_test_secret", 404},
		{"GET", "/v1/devices/a/credentials", "lwm_foreign_secret", 404},
		{"PUT", "/v1/devices/a/credentials", "lwm_read_secret", 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	get := deviceRequest(h, "GET", "/v1/devices/a/credentials", "")
	if strings.Contains(get.Body.String(), resp.PSK.Key) || strings.Contains(get.Body.String(), `"key"`) {
		t.Fatal("GET leaked secret")
	}
	if w = deviceRequest(h, "DELETE", "/v1/devices/a/credentials", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	ev, _, err := events.List("default", store.EventFilter{Type: "credentials.revoked", Limit: 100})
	if err != nil || len(ev) != 1 || strings.Contains(string(ev[0].Data), resp.PSK.Key) || strings.Contains(string(ev[0].Data), resp.PSK.Identity) {
		t.Fatal("bad revocation event", ev, err)
	}
}

func TestCredentialsPSKLifecycle(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Server: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	secret := "000102030405060708090a0b0c0d0e0f"
	w := deviceRequest(h, "PUT", "/v1/devices/pump/credentials", `{"psk":{"identity":"pump-id","key":"hex:`+secret+`"}}`)
	if w.Code != 200 && w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/devices/pump/credentials", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"identity":"pump-id"`) || !strings.Contains(w.Body.String(), `"updatedAt"`) || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), `"key"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = deviceRequest(h, "DELETE", "/v1/devices/pump/credentials", "")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := s.Security().ByEndpoint("pump"); ok {
		t.Fatal("credential survived deletion")
	}
}
