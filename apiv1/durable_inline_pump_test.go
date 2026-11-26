package apiv1

import (
	"crypto/sha256"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDurableInlinePump(t *testing.T) {
	devices := &memory.Devices{}
	srv := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = srv.Close() })
	keys := Keys{entries: map[string]credential{"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: devices, Server: srv})
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
		r.Header.Set("Idempotency-Key", "op")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := call()
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	if _, ok := srv.Security().ByEndpoint("pump"); !ok {
		t.Fatal("missing installed credential")
	}
	replay := call()
	if replay.Code != 201 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
}

func TestInlineWithoutOwnedSecurityFailsClosed(t *testing.T) {
	d := &memory.Devices{}
	s := server.New(server.Config{})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s})
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 501 || !strings.Contains(w.Body.String(), "inline_credentials_unavailable") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if _, err := d.Get("default", "pump"); err == nil {
		t.Fatal("device was written")
	}
	if _, ok := s.Security().ByEndpoint("pump"); ok {
		t.Fatal("credential was written")
	}
}

func TestInlineConflictDoesNotInstallCredential(t *testing.T) {
	d := &memory.Devices{}
	if _, err := d.Create("other", store.Device{Endpoint: "pump"}); err != nil {
		t.Fatal(err)
	}
	s := server.New(server.Config{Security: d.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s})
	r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(`{"endpoint":"pump","credentials":{"psk":{"identity":"attacker","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`))
	r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if _, ok := s.Security().ByEndpoint("pump"); ok {
		t.Fatal("conflict installed credential")
	}
}

func TestDeviceCreatePutInlineFailsClosed(t *testing.T) {
	d := &memory.Devices{}
	s := server.New(server.Config{Security: d.Security()})
	t.Cleanup(func() { _ = s.Close() })
	keys := Keys{entries: map[string]credential{"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	h := New(Options{Keys: keys, Devices: d, Server: s})
	r := httptest.NewRequest("PUT", "/v1/device-creates/op", strings.NewReader(`{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`))
	r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 501 || !strings.Contains(w.Body.String(), "inline_credentials_unavailable") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if _, err := d.Get("default", "pump"); err == nil {
		t.Fatal("device created")
	}
	if _, ok := s.Security().ByEndpoint("pump"); ok {
		t.Fatal("credential installed")
	}
}
