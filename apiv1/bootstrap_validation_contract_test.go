package apiv1

import (
	"crypto/sha256"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"strings"
	"testing"
)

func TestBootstrapValidation(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`)
	for _, tc := range []struct {
		name, body, field string
		status            int
	}{
		{"duplicate-server", `{"servers":[{"shortServerId":1},{"shortServerId":1}]}`, "shortServerId", 422},
		{"duplicate-security-instance", `{"servers":[{"shortServerId":1,"securityInstance":0},{"shortServerId":2,"securityInstance":0}]}`, "securityInstance", 422},
		{"duplicate-server-instance", `{"servers":[{"shortServerId":1,"serverInstance":0},{"shortServerId":2,"serverInstance":0}]}`, "serverInstance", 422},
		{"acl-owner", `{"servers":[{"shortServerId":1}],"acls":[{"owner":2}]}`, "owner", 422},
		{"psk-prefix", `{"servers":[{"shortServerId":1,"security":{"psk":{"key":"000102030405060708090a0b0c0d0e0f"}}}]}`, "key", 422},
		{"psk-short", `{"servers":[{"shortServerId":1,"security":{"psk":{"key":"hex:00"}}}]}`, "key", 422},
		{"psk-invalid", `{"servers":[{"shortServerId":1,"security":{"psk":{"key":"base64:!"}}}]}`, "key", 422},
		{"rpk", `{"servers":[{"shortServerId":1,"security":{"rpk":{}}}]}`, "rpk_unsupported", 501},
		{"generate", `{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true}}}]}`, "psk_generate_unsupported", 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", tc.body)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.field) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestBootstrapIsolationAndScope(t *testing.T) {
	keys := Keys{entries: map[string]credential{
		"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"},
	}}
	devices := &memory.Devices{}
	configs := NewMemoryBootstrapConfigStore()
	h := New(Options{Keys: keys, Devices: devices, BootstrapConfigs: configs})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`)
	deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", `{}`)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		w := deviceRequest(h, method, "/v1/devices/missing/bootstrap", `{}`)
		if w.Code != 404 {
			t.Fatal(method, w.Code)
		}
	}
	c := keys.entries["test"]
	c.tenant = "other"
	keys.entries["test"] = c
	h = New(Options{Keys: keys, Devices: devices, BootstrapConfigs: configs})
	if w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	c.scopes = ",devices:read,devices:write,"
	keys.entries["test"] = c
	h = New(Options{Keys: keys})
	for _, method := range []string{"PUT", "DELETE"} {
		if w := deviceRequest(h, method, "/v1/devices/a/bootstrap", `{}`); w.Code != 403 {
			t.Fatal(method, w.Code)
		}
	}
}
