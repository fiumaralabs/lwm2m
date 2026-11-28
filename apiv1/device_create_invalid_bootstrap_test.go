package apiv1

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateInvalidBootstrapOutcome(t *testing.T) {
	d := &memory.Devices{}
	s := server.New(server.Config{Security: d.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,credentials:write,bootstrap:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s})
	bodies := []string{
		`{"endpoint":"probe","bootstrap":null}`,
		`{"endpoint":"probe","bootstrap":{"servers":"bad"}}`,
		`{"endpoint":"probe","bootstrap":{"servers":[{}]}}`,
		`{"endpoint":"probe","bootstrap":{"servers":[]},"credentials":null}`,
		`{"endpoint":"probe","bootstrap":{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true}}}]}}`,
	}
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := fmt.Sprintf("/v1/device-creates/invalid-bootstrap-%d", i)
			first := deviceRequest(h, "PUT", path, body)
			if first.Code != 200 || !strings.Contains(first.Body.String(), `"status":"not-created"`) || !strings.Contains(first.Body.String(), `"reason":"invalid"`) {
				t.Fatalf("initial: %d %s", first.Code, first.Body)
			}
			for _, method := range []string{"GET", "PUT"} {
				got := deviceRequest(h, method, path, body)
				if got.Code != 200 || got.Body.String() != first.Body.String() {
					t.Fatalf("%s: %d %s", method, got.Code, got.Body)
				}
			}
			changed := deviceRequest(h, "PUT", path, `{"endpoint":"other","bootstrap":{"servers":[]}}`)
			if changed.Code != 409 {
				t.Fatalf("changed: %d %s", changed.Code, changed.Body)
			}
		})
	}
	if _, err := d.Get("default", "probe"); err == nil {
		t.Fatal("invalid request created device")
	}
}
