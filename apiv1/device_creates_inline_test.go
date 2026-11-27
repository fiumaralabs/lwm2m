package apiv1

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeviceCreateInlinePSKReplay(t *testing.T) {
	devices := &memory.Devices{}
	srv := server.New(server.Config{Security: devices.Security()})
	t.Cleanup(func() { _ = srv.Close() })
	keys := Keys{entries: map[string]credential{
		"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"},
		"limited": {hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",devices:write,", tenant: "default"},
	}}
	h := New(Options{Keys: keys, Devices: devices, Server: srv})
	call := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/v1/device-creates/inline", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	first := call("lwm_allowed_secret", body)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	if _, ok := srv.Security().ByEndpoint("pump"); !ok {
		t.Fatal("credential missing")
	}
	replay := call("lwm_allowed_secret", body)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	mismatch := call("lwm_allowed_secret", strings.Replace(body, "00010203", "10010203", 1))
	if mismatch.Code != 409 {
		t.Fatalf("mismatch: %d %s", mismatch.Code, mismatch.Body)
	}
	denied := call("lwm_limited_secret", body)
	if denied.Code != 403 {
		t.Fatalf("scope: %d %s", denied.Code, denied.Body)
	}
	denied = call("lwm_limited_secret", `{"endpoint":"pump"}`)
	if denied.Code != 403 {
		t.Fatalf("stored outcome scope: %d %s", denied.Code, denied.Body)
	}
}

func TestDeviceCreateRejectsAmbiguousCredentialKeys(t *testing.T) {
	for _, fields := range []string{
		`"Credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}`,
		`"CREDENTIALS":null`,
		`"credentials":null,"Credentials":{"psk":{}}`,
		`"Credentials":{"psk":{}},"credentials":null`,
		`"credentials":null,"credentials":{"psk":{}}`,
	} {
		for _, token := range []string{"lwm_limited_secret", "lwm_allowed_secret"} {
			t.Run(fields+token, func(t *testing.T) {
				devices := &memory.Devices{}
				keys := Keys{entries: map[string]credential{
					"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"},
					"limited": {hash: sha256.Sum256([]byte("lwm_limited_secret")), scopes: ",devices:write,", tenant: "default"},
				}}
				h := New(Options{Keys: keys, Devices: devices})
				r := httptest.NewRequest("PUT", "/v1/device-creates/probe", strings.NewReader(`{"endpoint":"pump",`+fields+`}`))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, body = %s", w.Code, w.Body)
				}
				if _, err := devices.GetDeviceCreate(context.Background(), "default", "probe"); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("unexpected outcome: %v", err)
				}
			})
		}
	}
}

type racingCreateStore struct {
	*memory.Devices
	lookups atomic.Int32
	ready   chan struct{}
}

func (s *racingCreateStore) GetDeviceCreate(ctx context.Context, tenant, id string) (store.DeviceCreateOutcome, error) {
	result, err := s.Devices.GetDeviceCreate(ctx, tenant, id)
	n := s.lookups.Add(1)
	if n <= 2 {
		if n == 2 {
			close(s.ready)
		}
		<-s.ready
	}
	return result, err
}

func TestDeviceCreateInlineConcurrentRouters(t *testing.T) {
	d := &racingCreateStore{Devices: &memory.Devices{}, ready: make(chan struct{})}
	srv := server.New(server.Config{Security: d.Security()})
	t.Cleanup(func() { _ = srv.Close() })
	keys := Keys{entries: map[string]credential{"allowed": {hash: sha256.Sum256([]byte("lwm_allowed_secret")), scopes: ",devices:write,credentials:write,", tenant: "default"}}}
	body := `{"endpoint":"race","credentials":{"psk":{"identity":"race","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	handlers := []http.Handler{New(Options{Keys: keys, Devices: d, Server: srv}), New(Options{Keys: keys, Devices: d, Server: srv})}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, h := range handlers {
		wg.Add(1)
		go func(h http.Handler) {
			defer wg.Done()
			r := httptest.NewRequest("PUT", "/v1/device-creates/race", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer lwm_allowed_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			statuses <- w.Code
		}(h)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[201] != 1 || counts[200] != 1 {
		t.Fatalf("statuses: %v", counts)
	}
}
