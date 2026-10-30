package apiv1

import (
	"strings"
	"testing"
)

func TestBootstrapRawSecurityWriteSecretsAreRedacted(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`)
	body := `{"servers":[{"shortServerId":1,"security":{"psk":{"key":"hex:000102030405060708090a0b0c0d0e0f"}}}],"writes":[{"path":"/0/0","values":[{"path":"/0/0/5","value":"raw-secret"}]},{"path":"/23/0","values":[{"path":"/23/0/0","value":"public-value"}]}]}`
	if w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "raw-secret") || strings.Contains(w.Body.String(), "0001020304050607") || !strings.Contains(w.Body.String(), "public-value") {
		t.Fatal(w.Code, w.Body.String())
	}
}
