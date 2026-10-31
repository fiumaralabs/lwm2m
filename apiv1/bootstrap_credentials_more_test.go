package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestBootstrapCredentialsContract(t *testing.T) {
	devices := &memory.Devices{}
	_, _ = devices.Create("default", store.Device{Endpoint: "pump"})
	keys := Keys{entries: map[string]credential{
		"test":    {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,bootstrap:write,", tenant: "default"},
		"foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,bootstrap:write,", tenant: "other"},
		"read":    {hash: sha256.Sum256([]byte("lwm_read_secret")), scopes: ",devices:read,", tenant: "default"},
		"write":   {hash: sha256.Sum256([]byte("lwm_write_secret")), scopes: ",bootstrap:write,", tenant: "default"},
	}}
	cs := NewMemoryBootstrapCredentialStore()
	h := Authenticate(keys, NewBootstrapCredentialsHandler(devices, cs))
	call := func(method, ep, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/devices/"+ep+"/bootstrap/credentials", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_"+token+"_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s %s: got %d want %d: %s", method, ep, token, w.Code, status, w.Body.String())
		}
		return w
	}
	const valid = `{"psk":{"identity":"factory","key":"hex:000102030405060708090a0b0c0d0e0f"}}`
	for _, body := range []string{`{}`, `{"psk":{"identity":"x","key":"hex:00"}}`, `{"psk":{"identity":"","key":"hex:000102030405060708090a0b0c0d0e0f"}}`, `{"psk":{"generate":true,"identity":"x"}}`, `{`} {
		call("PUT", "pump", "test", body, 400)
	}
	call("PUT", "pump", "test", `{"rpk":{}}`, 501)
	call("PUT", "missing", "test", valid, 404)
	call("PUT", "pump", "foreign", valid, 404)
	call("PUT", "pump", "read", valid, 403)
	call("GET", "pump", "write", "", 403)
	call("PUT", "pump", "test", valid, 201)
	call("PUT", "pump", "test", `{"psk":{"identity":"replacement","key":"hex:000102030405060708090a0b0c0d0e0f"}}`, 200)
	w := call("PUT", "pump", "test", `{"psk":{"generate":true}}`, 200)
	var got credentialResponse
	if json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Body.String())
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got.PSK.Key, "base64:"))
	if err != nil || len(raw) != 32 || got.PSK.Identity == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid generated credential")
	}
	w = call("GET", "pump", "test", "", 200)
	if strings.Contains(w.Body.String(), got.PSK.Key) || strings.Contains(w.Body.String(), `"key"`) {
		t.Fatal("secret leaked")
	}
	call("DELETE", "pump", "foreign", "", 404)
	call("DELETE", "pump", "test", "", 204)
	call("GET", "pump", "test", "", 404)
	call("DELETE", "pump", "test", "", 404)
}

func TestBootstrapCredentialStoreIsolationAndCopies(t *testing.T) {
	s := NewMemoryBootstrapCredentialStore()
	key := []byte("0123456789abcdef")
	if existed, duplicate := s.Put("a", "ep", BootstrapCredential{Identity: "one", Key: key}); existed || duplicate {
		t.Fatal("unexpected put result")
	}
	key[0] = 'x'
	c, ok := s.Get("a", "ep")
	if !ok || string(c.Key) != "0123456789abcdef" {
		t.Fatal("put alias")
	}
	c.Key[0] = 'x'
	c, _ = s.Get("a", "ep")
	if string(c.Key) != "0123456789abcdef" {
		t.Fatal("get alias")
	}
	if _, ok := s.Get("b", "ep"); ok {
		t.Fatal("tenant leak")
	}
	if _, duplicate := s.Put("b", "ep", BootstrapCredential{Identity: "one"}); !duplicate {
		t.Fatal("duplicate accepted")
	}
}
