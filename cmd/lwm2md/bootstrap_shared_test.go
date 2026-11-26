package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
)

func TestDaemonBootstrapSharedStore(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices = &memory.Devices{}
			if backend == "sqlite" {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				devices = db
			}
			seam := devices.(store.DeviceBootstrapCreateStore)
			_, _, err := seam.CreateDeviceWithSecurityAndBootstrap(context.Background(), "default", "op", "fp", store.Device{Endpoint: "pump"}, server.SecurityInfo{}, json.RawMessage(`{"servers":[{"shortServerId":1}]}`))
			if err != nil {
				t.Fatal(err)
			}
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,bootstrap:write", sha256.Sum256([]byte("lwm_test_secret")))))
			if err != nil {
				t.Fatal(err)
			}
			srv, h, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, devices)
			defer srv.Close()
			defer executor.Close()
			defer dispatcher.Close()
			call := func(method, body string, code int, contains string) {
				t.Helper()
				r := httptest.NewRequest(method, "/v1/devices/pump/bootstrap", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_test_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != code || !strings.Contains(w.Body.String(), contains) {
					t.Fatalf("%s %d %s", method, w.Code, w.Body.String())
				}
			}
			call("GET", "", 200, `"shortServerId":1`)
			call("PUT", `{}`, 204, "")
			b, ok, err := seam.GetBootstrapConfig(context.Background(), "default", "pump")
			if err != nil || !ok || string(b) != `{}` {
				t.Fatalf("seam read %s %v %v", b, ok, err)
			}
			call("DELETE", "", 204, "")
			_, ok, err = seam.GetBootstrapConfig(context.Background(), "default", "pump")
			if err != nil || ok {
				t.Fatalf("seam delete %v %v", ok, err)
			}
		})
	}
}
