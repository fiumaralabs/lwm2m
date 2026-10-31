package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestBootstrapGeneratedPSK(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	keys := Keys{entries: map[string]credential{
		"test":    {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"},
		"foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "other"},
	}}
	configs := NewMemoryBootstrapConfigStore()
	h := New(Options{Keys: keys, Server: s, BootstrapConfigs: configs})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	body := `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}],"extra":9007199254740993}`
	var previous string
	for i := 0; i < 2; i++ {
		w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body)
		if w.Code != 200 {
			t.Fatalf("generate status %d", w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		var result struct {
			Servers []struct {
				Security struct {
					PSK struct {
						Identity string `json:"identity"`
						Key      string `json:"key"`
					} `json:"psk"`
				} `json:"security"`
			} `json:"servers"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Servers) != 1 {
			t.Fatal("invalid response")
		}
		p := result.Servers[0].Security.PSK
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.Key, "base64:"))
		if p.Identity == "" || !strings.HasPrefix(p.Key, "base64:") || err != nil || len(key) != 32 || p.Key == previous {
			t.Fatal("invalid or repeated generated credential")
		}
		previous = p.Key
		info, ok := s.Security().ByPSKIdentity(p.Identity)
		if !ok || info.Endpoint != "a" || string(info.PSKKey) != string(key) {
			t.Fatal("server credential mismatch")
		}
		cred := deviceRequest(h, "GET", "/v1/devices/a/credentials", "")
		if cred.Code != 200 || !strings.Contains(cred.Body.String(), p.Identity) || strings.Contains(cred.Body.String(), `"key"`) {
			t.Fatal("credential GET mismatch")
		}
		get := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", "")
		if get.Code != 200 || strings.Contains(get.Body.String(), p.Key) || strings.Contains(get.Body.String(), `"key"`) || !strings.Contains(get.Body.String(), "9007199254740993") {
			t.Fatal("bootstrap GET mismatch")
		}
		raw, ok := configs.Get("default", "a")
		if !ok || !strings.Contains(string(raw), p.Key) {
			t.Fatal("generated key not stored")
		}
	}
	r := httptest.NewRequest("GET", "/v1/devices/a/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer lwm_foreign_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("foreign tenant got %d", w.Code)
	}
}

func TestBootstrapGeneratedPSKIsolationAndRedaction(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	keys := Keys{entries: map[string]credential{
		"test":    {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"},
		"foreign": {hash: sha256.Sum256([]byte("lwm_foreign_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "other"},
	}}
	h := New(Options{Server: s, Keys: keys})
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"lwm_test_secret", "lwm_foreign_secret"} {
		if w := request("POST", "/v1/devices", `{"endpoint":"a"}`, token); w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	body := `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}},{"shortServerId":2,"security":{"psk":{"identity":"external","key":"hex:000102030405060708090a0b0c0d0e0f"}}}]}`
	if w := request("PUT", "/v1/devices/a/bootstrap", body, "lwm_foreign_secret"); w.Code != 404 {
		t.Fatalf("foreign self: %d", w.Code)
	}
	if _, ok := s.Security().ByEndpoint("a"); ok {
		t.Fatal("foreign tenant changed server credentials")
	}
	w := request("PUT", "/v1/devices/a/bootstrap", body, "lwm_test_secret")
	if w.Code != 200 || strings.Contains(w.Body.String(), "000102030405060708090a0b0c0d0e0f") {
		t.Fatal("supplied secret leaked")
	}
	foreign := request("GET", "/v1/devices/a/bootstrap", "", "lwm_foreign_secret")
	if foreign.Code != 404 {
		t.Fatal("foreign tenant saw config")
	}
	invalid := `{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true,"identity":"provided"}}}]}`
	if w := request("PUT", "/v1/devices/a/bootstrap", invalid, "lwm_test_secret"); w.Code != 422 {
		t.Fatalf("invalid generate: %d", w.Code)
	}
}
