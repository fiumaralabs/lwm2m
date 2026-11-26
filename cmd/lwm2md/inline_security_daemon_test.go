package main

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestDaemonInlineCredentialsWithRevokingStore(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices
			var security server.SecurityStore
			if backend == "memory" {
				d := &memory.Devices{}
				devices, security = d, d.Security()
			} else {
				d, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				devices, security = d, d.Security()
			}
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,credentials:write", sha256.Sum256([]byte("lwm_test_secret")))))
			if err != nil {
				t.Fatal(err)
			}
			wrapped := coap.NewRevokingSecurityStore(security, func(string) {})
			srv, handler, executor, dispatcher := newDaemon(server.Config{Security: wrapped}, leshanapi.New(leshanapi.Options{}), keys, devices)
			defer srv.Close()
			defer executor.Close()
			defer dispatcher.Close()
			body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
			call := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_test_secret")
				r.Header.Set("Idempotency-Key", "one")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			first := call()
			if first.Code != 201 {
				t.Fatalf("first: %d %s", first.Code, first.Body)
			}
			if _, ok := srv.Security().ByEndpoint("pump"); !ok {
				t.Fatal("missing credential in daemon security store")
			}
			again := call()
			if again.Code != 201 || again.Body.String() != first.Body.String() {
				t.Fatalf("replay: %d %s", again.Code, again.Body)
			}
		})
	}
}
