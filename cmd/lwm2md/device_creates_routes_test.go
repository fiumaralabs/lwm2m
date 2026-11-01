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
)

func TestDeviceCreateDaemonStores(t *testing.T) {
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
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write", sha256.Sum256([]byte("lwm_test_secret")))))
			if err != nil {
				t.Fatal(err)
			}
			srv, h, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, devices)
			defer srv.Close()
			defer executor.Close()
			defer dispatcher.Close()
			call := func(method, id, body string, want int, text string) string {
				t.Helper()
				r := httptest.NewRequest(method, "/v1/device-creates/"+id, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_test_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != want || !strings.Contains(w.Body.String(), text) {
					t.Fatalf("%s %s: %d %s", method, id, w.Code, w.Body.String())
				}
				return w.Body.String()
			}
			first := call("PUT", "one", `{"endpoint":"pump"}`, 201, `"status":"committed"`)
			if got := call("PUT", "one", `{"endpoint":"pump"}`, 200, `"status":"committed"`); got != first {
				t.Fatal("replay changed")
			}
			if db, ok := devices.(*sqlite.Devices); ok {
				entries, err := db.Audit().List("default")
				if err != nil || len(entries) != 1 {
					t.Fatalf("audit: %d %v", len(entries), err)
				}
				events, _, err := db.Events().List("default", store.EventFilter{Type: "lwm2m.device.created", Limit: 100})
				if err != nil || len(events) != 1 {
					t.Fatalf("events: %d %v", len(events), err)
				}
			}
			call("PUT", "one", `{"endpoint":"different"}`, 409, `operation-id-reused`)
			call("PUT", "conflict", `{"endpoint":"pump"}`, 200, `"reason":"endpoint-conflict"`)
			call("PUT", "invalid", `{"unknown":"synthetic-psk-value"}`, 200, `"reason":"invalid"`)
			call("GET", "fence", "", 200, `"reason":"fenced"`)
			call("PUT", "fence", `{"endpoint":"blocked"}`, 200, `"reason":"fenced"`)
			call("DELETE", "fence", "", 204, "")
			call("DELETE", "fence", "", 404, `not_found`)
		})
	}
}

func TestDeviceCreateTenantIsolationStores(t *testing.T) {
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
			keyStore := &memory.APIKeys{}
			for _, id := range []string{"a", "b"} {
				err := keyStore.Create(store.APIKey{KeyMetadata: store.KeyMetadata{ID: id, Scopes: []string{"devices:write"}}, Hash: sha256.Sum256([]byte("lwm_" + id + "_secret")), Tenant: id})
				if err != nil {
					t.Fatal(err)
				}
			}
			h := apiv1.New(apiv1.Options{Devices: devices, Keys: (apiv1.Keys{}).WithStore(keyStore)})
			call := func(id, method, op, body string, want int, content string) {
				t.Helper()
				r := httptest.NewRequest(method, "/v1/device-creates/"+op, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_"+id+"_secret")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != want || !strings.Contains(w.Body.String(), content) {
					t.Fatalf("%s %s %s: %d %s", id, method, op, w.Code, w.Body.String())
				}
			}
			call("a", "PUT", "a-only", `{"endpoint":"private-a"}`, 201, `"status":"committed"`)
			call("b", "GET", "a-only", "", 200, `"reason":"fenced"`)
			call("b", "PUT", "collision", `{"endpoint":"private-a"}`, 200, `"reason":"endpoint-conflict"`)
			call("a", "PUT", "shared", `{"endpoint":"first"}`, 201, `"status":"committed"`)
			call("b", "PUT", "shared", `{"endpoint":"second"}`, 201, `"status":"committed"`)
		})
	}
}
