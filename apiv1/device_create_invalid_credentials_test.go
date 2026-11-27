package apiv1

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateInvalidCredentialOutcome(t *testing.T) {
	d := &memory.Devices{}
	s := server.New(server.Config{Security: d.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s})
	for i, cred := range []string{`{"psk":{"identity":"x","key":"bogus"}}`, `null`, `{"psk":{"generate":true}}`, `{"psk":{"identity":"x","key":"hex:00"}}`} {
		path := fmt.Sprintf("/v1/device-creates/invalid%d", i)
		body := `{"endpoint":"x","credentials":` + cred + `}`
		first := deviceRequest(h, "PUT", path, body)
		if first.Code != 200 || !strings.Contains(first.Body.String(), `"reason":"invalid"`) || !strings.Contains(first.Body.String(), `"fingerprint":"salted-sha256:`) || strings.Contains(first.Body.String(), "bogus") {
			t.Fatalf("initial %d: %d %s", i, first.Code, first.Body)
		}
		for _, method := range []string{"GET", "PUT"} {
			got := deviceRequest(h, method, path, body)
			if got.Code != 200 || got.Body.String() != first.Body.String() {
				t.Fatalf("%s %d: %d %s", method, i, got.Code, got.Body)
			}
		}
		got := deviceRequest(h, "PUT", path, `{"endpoint":"other","credentials":`+cred+`}`)
		if got.Code != 409 {
			t.Fatalf("changed %d: %d %s", i, got.Code, got.Body)
		}
	}
	if _, err := d.Get("default", "x"); err == nil {
		t.Fatal("invalid request created device")
	}
	unsupported := deviceRequest(h, "PUT", "/v1/device-creates/unsupported", `{"endpoint":"x","credentials":{"rpk":{}}}`)
	if unsupported.Code != 501 {
		t.Fatalf("unsupported mode: %d %s", unsupported.Code, unsupported.Body)
	}
	fenced := deviceRequest(h, "GET", "/v1/device-creates/unsupported", "")
	if !strings.Contains(fenced.Body.String(), `"reason":"fenced"`) {
		t.Fatalf("unsupported request recorded outcome: %s", fenced.Body)
	}
	limited := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:write,", tenant: "default"}}}
	limitedHandler := New(Options{Keys: limited, Devices: d, Server: s})
	denied := deviceRequest(limitedHandler, "PUT", "/v1/device-creates/denied", `{"endpoint":"x","credentials":{"psk":{"key":"bogus"}}}`)
	if denied.Code != 403 {
		t.Fatalf("missing scope: %d %s", denied.Code, denied.Body)
	}
	fenced = deviceRequest(h, "GET", "/v1/device-creates/denied", "")
	if !strings.Contains(fenced.Body.String(), `"reason":"fenced"`) {
		t.Fatalf("denied request recorded outcome: %s", fenced.Body)
	}
}
