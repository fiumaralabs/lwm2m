package apiv1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type crashReplayDevices struct {
	*memory.Devices
	crash          bool
	originalStatus int
	originalHeader http.Header
	originalBody   []byte
}

func (s *crashReplayDevices) ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error) {
	return s.Devices.ReplayDeviceCreate(ctx, tenant, key, func() (int, http.Header, []byte) {
		status, header, body := run()
		if s.crash {
			s.crash = false
			s.originalStatus, s.originalHeader, s.originalBody = status, header.Clone(), append([]byte(nil), body...)
			panic("injected crash after device commit")
		}
		return status, header, body
	})
}

func TestDeviceCreateReplayCrashWindow(t *testing.T) {
	d := &crashReplayDevices{Devices: &memory.Devices{}, crash: true}
	if _, err := d.Create("other", store.Device{Endpoint: "crash"}); err != nil {
		t.Fatal(err)
	}
	post := func() *httptest.ResponseRecorder {
		h := New(Options{Keys: deviceKeys(), Devices: d})
		r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(`{"endpoint":"crash"}`))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "crash-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	func() { defer func() { _ = recover() }(); post() }()
	if d.originalStatus != 201 {
		t.Fatalf("initial create: %d %s", d.originalStatus, d.originalBody)
	}
	retry := post()
	if retry.Code != d.originalStatus || retry.Body.String() != string(d.originalBody) || retry.Header().Get("Location") != d.originalHeader.Get("Location") {
		t.Fatalf("replay after crash: got %d %s, want %d %s", retry.Code, retry.Body, d.originalStatus, d.originalBody)
	}
	for _, tenant := range []string{"default", "other"} {
		devices, _, err := d.List(tenant, store.DeviceFilter{}, "", 100)
		if err != nil || len(devices) != 1 {
			t.Fatalf("%s devices: %v, %v", tenant, devices, err)
		}
	}
}
