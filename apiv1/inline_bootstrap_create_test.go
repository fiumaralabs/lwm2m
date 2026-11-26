package apiv1_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/fiumaralabs/lwm2m/apiv1"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestInlineBootstrapCreateIsReadable(t *testing.T) {
	devices := &memory.Devices{}
	s := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = s.Close() })
	hash := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write,bootstrap:write", hash)))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Devices: devices, Server: s})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}},"bootstrap":{"servers":[]}}`
	if w := call("POST", "/v1/devices", body); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := call("GET", "/v1/devices/pump/bootstrap", ""); w.Code != 200 {
		t.Fatalf("bootstrap GET: %d %s", w.Code, w.Body)
	}
}

// failingBootstrap simulates a store error before its atomic transaction commits.
type failingBootstrap struct {
	store.Devices
	store.DeviceSecurityCreateStore
	store.DeviceCreateStore
	store.DeviceBootstrapCreateStore
	security server.SecurityStore
	fail     bool
}

func (f *failingBootstrap) OwnsSecurity(s server.SecurityStore) bool { return s == f.security }
func (f *failingBootstrap) CreateDeviceWithSecurityAndBootstrap(ctx context.Context, tenant, id, fingerprint string, d store.Device, sec server.SecurityInfo, config json.RawMessage) (store.DeviceCreateOutcome, bool, error) {
	if f.fail {
		return store.DeviceCreateOutcome{}, false, errors.New("injected failure")
	}
	return f.DeviceBootstrapCreateStore.CreateDeviceWithSecurityAndBootstrap(ctx, tenant, id, fingerprint, d, sec, config)
}
func TestInlineBootstrapGeneratedAndReplayAuthorization(t *testing.T) {
	devices := &memory.Devices{}
	srv := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = srv.Close() })
	a := sha256.Sum256([]byte("lwm_test_secret"))
	b := sha256.Sum256([]byte("lwm_limited_secret"))
	keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write,bootstrap:write\nkey_limited sha256:%x devices:read,devices:write,credentials:write", a, b)))
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Devices: devices, Server: srv})
	body := `{"endpoint":"generated","bootstrap":{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}]}}`
	call := func(token, data string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "generated")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := call("lwm_test_secret", body)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	sec, ok := devices.Security().ByEndpoint("generated")
	if !ok || len(sec.PSKKey) == 0 {
		t.Fatal("missing generated credential")
	}
	if strings.Contains(first.Body.String(), sec.PSKIdentity) || strings.Contains(first.Body.String(), "psk") {
		t.Fatal("credential echoed")
	}
	replay := call("lwm_limited_secret", body)
	if replay.Code != 403 {
		t.Fatalf("unauthorized replay: %d %s", replay.Code, replay.Body)
	}
}

func TestInlineBootstrapStores(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices
			var security server.SecurityStore
			if backend == "memory" {
				d := &memory.Devices{}
				devices, security = d, d.Security()
			} else {
				d, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				devices, security = d, d.Security()
			}
			wrapped := &failingBootstrap{Devices: devices, DeviceSecurityCreateStore: devices.(store.DeviceSecurityCreateStore), DeviceCreateStore: devices.(store.DeviceCreateStore), DeviceBootstrapCreateStore: devices.(store.DeviceBootstrapCreateStore), security: security}
			srv := server.New(server.Config{Security: security})
			t.Cleanup(func() { _ = srv.Close() })
			hash := sha256.Sum256([]byte("lwm_test_secret"))
			keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write,bootstrap:write", hash)))
			if err != nil {
				t.Fatal(err)
			}
			managed := &memory.APIKeys{}
			if err := managed.Create(store.APIKey{KeyMetadata: store.KeyMetadata{ID: "other", Scopes: []string{"devices:read", "devices:write", "bootstrap:write"}}, Hash: sha256.Sum256([]byte("lwm_other_secret")), Tenant: "other"}); err != nil {
				t.Fatal(err)
			}
			keys = keys.WithStore(managed)
			h := New(Options{Keys: keys, Devices: wrapped, Server: srv})
			post := func(token, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			body := func(ep, identity, config string) string {
				return `{"endpoint":"` + ep + `","credentials":{"psk":{"identity":"` + identity + `","key":"hex:000102030405060708090a0b0c0d0e0f"}},"bootstrap":` + config + `}`
			}
			checkAbsent := func(ep string) {
				t.Helper()
				if _, err := devices.Get("default", ep); err == nil {
					t.Fatal("device persisted", ep)
				}
				if _, ok := security.ByEndpoint(ep); ok {
					t.Fatal("credential persisted", ep)
				}
				if _, ok, err := wrapped.GetBootstrapConfig(context.Background(), "default", ep); err != nil || ok {
					t.Fatalf("config persisted %s %v", ep, err)
				}
			}
			invalid := post("lwm_test_secret", body("bad", "bad", `{"servers":[{}]}`))
			if invalid.Code != 400 {
				t.Fatalf("invalid: %d %s", invalid.Code, invalid.Body)
			}
			checkAbsent("bad")
			if err := wrapped.PutBootstrapConfig(context.Background(), "default", "orphan", json.RawMessage(`{"servers":[]}`)); err != nil {
				t.Fatal(err)
			}
			conflict := post("lwm_test_secret", body("orphan", "orphan", `{"servers":[]}`))
			if conflict.Code != 409 {
				t.Fatalf("config conflict: %d %s", conflict.Code, conflict.Body)
			}
			if _, err := devices.Get("default", "orphan"); err == nil {
				t.Fatal("conflict created device")
			}
			if _, ok := security.ByEndpoint("orphan"); ok {
				t.Fatal("conflict created credential")
			}
			wrapped.fail = true
			failed := post("lwm_test_secret", body("failed", "failed", `{"servers":[]}`))
			wrapped.fail = false
			if failed.Code != 500 {
				t.Fatalf("failure: %d %s", failed.Code, failed.Body)
			}
			checkAbsent("failed")
			good := post("lwm_test_secret", body("good", "shared", `{"servers":[]}`))
			if good.Code != 201 || strings.Contains(good.Body.String(), "000102030405") || strings.Contains(good.Body.String(), "shared") {
				t.Fatalf("create: %d %s", good.Code, good.Body)
			}
			duplicate := post("lwm_test_secret", body("duplicate", "shared", `{"servers":[]}`))
			if duplicate.Code != 409 {
				t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body)
			}
			checkAbsent("duplicate")
			other := post("lwm_other_secret", `{"endpoint":"other-device","bootstrap":{"servers":[]}}`)
			if other.Code != 201 {
				t.Fatalf("other tenant: %d %s", other.Code, other.Body)
			}
			if _, ok, err := wrapped.GetBootstrapConfig(context.Background(), "default", "other-device"); err != nil || ok {
				t.Fatal("other tenant config leaked")
			}
			if _, ok, err := wrapped.GetBootstrapConfig(context.Background(), "other", "other-device"); err != nil || !ok {
				t.Fatal("other tenant config missing")
			}
			if _, ok, err := wrapped.GetBootstrapConfig(context.Background(), "other", "good"); err != nil || ok {
				t.Fatal("tenant config leaked")
			}
		})
	}
}
