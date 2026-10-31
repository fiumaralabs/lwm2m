package apiv1

import (
	"crypto/sha256"
	"github.com/fiumaralabs/lwm2m/server"
	"strings"
	"testing"
)

func TestBootstrapGeneratedPSKRejectsAmbiguousJSON(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,bootstrap:write,", tenant: "default"}}}
	h := New(Options{Server: s, Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	cases := []string{
		`{"Servers":[{"shortServerId":1,"security":{"psk":{"generate":true}}}]}`,
		`{"servers":[{"shortServerId":1,"Security":{"psk":{"generate":true}}}]}`,
		`{"servers":[{"shortServerId":1,"security":{"PSK":{"generate":true}}}]}`,
		`{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true}}}],"Servers":null}`,
		`{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true,"generate":false}}}]}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			w := deviceRequest(h, "PUT", "/v1/devices/a/bootstrap", body)
			if w.Code != 400 {
				t.Fatalf("expected 400, got %d", w.Code)
			}
			get := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", "")
			if get.Code != 404 || strings.Contains(get.Body.String(), "base64:") {
				t.Fatal("rejected config stored")
			}
		})
	}
}
