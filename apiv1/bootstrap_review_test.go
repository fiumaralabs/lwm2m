package apiv1

import (
	"strings"
	"testing"
)

func TestBootstrapGETRedactsSupportedSecuritySecrets(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	body := `{"servers":[{"shortServerId":1,"security":{"psk":{"identity":"visible-id","key":"hex:000102030405060708090a0b0c0d0e0f"},"x509":{"certificate":"visible-cert","privateKey":"private-secret"},"oscore":{"masterSecret":"oscore-secret","masterSalt":"visible-salt","senderId":"visible-sender"}}}],"writes":[{"path":"/21/0/2","value":"raw-oscore-secret"},{"path":"/23/0/0","value":"visible-value"}]}`
	if w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Read with only devices:read, independent of the write privilege.
	c.scopes = ",devices:read,"
	keys.entries["test"] = c
	w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, s := range []string{"0001020304050607", "private-secret", "oscore-secret", "raw-oscore-secret"} {
		if strings.Contains(w.Body.String(), s) {
			t.Errorf("GET leaked %q: %s", s, w.Body.String())
		}
	}
	for _, s := range []string{"visible-id", "visible-cert", "visible-salt", "visible-sender", "visible-value"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Errorf("GET lost %q: %s", s, w.Body.String())
		}
	}
}

func TestBootstrapRemovedWhenDeviceRecreated(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	configs := NewMemoryBootstrapConfigStore()
	h := New(Options{Keys: keys, BootstrapConfigs: configs})
	for _, step := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"PUT", "/v1/devices/a/bootstrap", `{"servers":[{"shortServerId":1,"security":{"oscore":{"masterSecret":"old-secret"}}}]}`, 204},
		{"DELETE", "/v1/devices/a", "", 204},
		{"POST", "/v1/devices", `{"endpoint":"a"}`, 201},
		{"GET", "/v1/devices/a/bootstrap", "", 404},
	} {
		if w := deviceRequest(h, step.method, step.path, step.body); w.Code != step.status {
			t.Fatalf("%s %s: got %d, want %d: %s", step.method, step.path, w.Code, step.status, w.Body.String())
		}
	}
	if _, ok := configs.Get("default", "a"); ok {
		t.Fatal("deleted device bootstrap config remains in store")
	}
}
