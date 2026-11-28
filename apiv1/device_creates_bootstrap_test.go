package apiv1_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceCreatePutBootstrapSeparateLiveCredential(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices = &memory.Devices{}
			if backend == "sqlite" {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				devices = db
			}
			live := server.NewMemorySecurityStore()
			if err := live.Put(server.SecurityInfo{Endpoint: "orphan", PSKIdentity: "orphan", PSKKey: []byte("0123456789abcdef")}); err != nil {
				t.Fatal(err)
			}
			srv := server.New(server.Config{Security: live})
			t.Cleanup(func() { _ = srv.Close() })
			hash := sha256.Sum256([]byte("lwm_test_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,bootstrap:write", hash)))
			if err != nil {
				t.Fatal(err)
			}
			h := apiv1.New(apiv1.Options{Keys: keys, Devices: devices, Server: srv})
			r := httptest.NewRequest("PUT", "/v1/device-creates/op", strings.NewReader(`{"endpoint":"orphan","bootstrap":{"servers":[]}}`))
			r.Header.Set("Authorization", "Bearer lwm_test_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 409 || !strings.Contains(w.Body.String(), "credential_exists") {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
			if _, err := devices.Get("default", "orphan"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("device: %v", err)
			}
			if _, err := devices.(store.DeviceCreateStore).GetDeviceCreate(t.Context(), "default", "op"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("outcome: %v", err)
			}
			if _, exists, err := devices.(store.DeviceBootstrapCreateStore).GetBootstrapConfig(t.Context(), "default", "orphan"); err != nil || exists {
				t.Fatalf("bootstrap: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestDeviceCreatePutBootstrap(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices
			if backend == "memory" {
				devices = &memory.Devices{}
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				devices = db
			}
			full := sha256.Sum256([]byte("lwm_test_secret"))
			limited := sha256.Sum256([]byte("lwm_limited_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:write,bootstrap:write\nkey_limited sha256:%x devices:write\n", full, limited)))
			if err != nil {
				t.Fatal(err)
			}
			h := apiv1.New(apiv1.Options{Keys: keys, Devices: devices})
			call := func(token, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("PUT", "/v1/device-creates/op", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			generated := `{"endpoint":"generated","bootstrap":{"servers":[{"shortServerId":1,"security":{"psk":{"generate":true}}}]}}`
			if w := call("lwm_test_secret", generated); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_credential") || !strings.Contains(w.Body.String(), "340") {
				t.Fatalf("generated: %d %s", w.Code, w.Body)
			}
			for _, fields := range []string{`"Bootstrap":{}`, `"bootstrap":{},"bootstrap":{}`} {
				if w := call("lwm_test_secret", `{"endpoint":"pump",`+fields+`}`); w.Code != 400 {
					t.Fatalf("ambiguous: %d %s", w.Code, w.Body)
				}
			}

			body := `{"endpoint":"pump","bootstrap":{"servers":[{"shortServerId":1,"security":{"psk":{"identity":"client","key":"hex:000102030405060708090a0b0c0d0e0f"}}}]}}`
			if w := call("lwm_limited_secret", body); w.Code != 403 {
				t.Fatalf("scope: %d %s", w.Code, w.Body)
			}
			first := call("lwm_test_secret", body)
			if first.Code != 201 {
				t.Fatalf("create: %d %s", first.Code, first.Body)
			}
			if w := call("lwm_limited_secret", `{"endpoint":"pump"}`); w.Code != 403 {
				t.Fatalf("stored scope: %d %s", w.Code, w.Body)
			}
			outcome, err := devices.(store.DeviceCreateStore).GetDeviceCreate(t.Context(), "default", "op")
			if err != nil || !strings.HasPrefix(outcome.Fingerprint, "salted-bootstrap:") {
				t.Fatalf("fingerprint: %v %q", err, outcome.Fingerprint)
			}
			if w := call("lwm_test_secret", body); w.Code != 200 || w.Body.String() != first.Body.String() {
				t.Fatalf("replay: %d %s", w.Code, w.Body)
			}
			if w := call("lwm_test_secret", strings.Replace(body, "00010203", "10010203", 1)); w.Code != 409 {
				t.Fatalf("mismatch: %d %s", w.Code, w.Body)
			}
			_, ok, err := devices.(store.DeviceBootstrapCreateStore).GetBootstrapConfig(t.Context(), "default", "pump")
			if err != nil || !ok {
				t.Fatalf("bootstrap missing: %v", err)
			}
		})
	}
}
