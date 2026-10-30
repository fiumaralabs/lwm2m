package apiv1

import (
	"strings"
	"testing"
)

func TestBootstrapLifecycle(t *testing.T) {
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := "/v1/devices/a/bootstrap"
	for _, body := range []string{`{"servers":[{"shortServerId":1,"security":{"psk":{"identity":"device","key":"hex:000102030405060708090a0b0c0d0e0f"}}}]}`, `{"servers":[{"shortServerId":2,"security":{"x509":{"certificate":"cert","privateKey":"secret"}}}]}`} {
		if w := deviceRequest(h, "PUT", path, body); w.Code != 200 && w.Code != 204 {
			t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
		}
	}
	w := deviceRequest(h, "GET", path, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"shortServerId":2`) {
		t.Fatalf("GET: %d %s", w.Code, w.Body.String())
	}
	if w = deviceRequest(h, "DELETE", path, ""); w.Code != 204 {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body.String())
	}
	if w = deviceRequest(h, "GET", path, ""); w.Code != 404 {
		t.Fatalf("GET after DELETE: %d", w.Code)
	}
}
