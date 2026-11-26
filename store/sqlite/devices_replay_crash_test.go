package sqlite_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type crashingDevices struct {
	*sqlite.Devices
	crash  bool
	status int
	header http.Header
	body   []byte
}

func (d *crashingDevices) ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error) {
	return d.Devices.ReplayDeviceCreate(ctx, tenant, key, func() (int, http.Header, []byte) {
		status, header, body := run()
		if d.crash {
			d.crash = false
			d.status = status
			d.header = header.Clone()
			d.body = append([]byte(nil), body...)
			panic("crash after commit")
		}
		return status, header, body
	})
}

func TestDeviceCreateReplaySurvivesSQLiteReopen(t *testing.T) {
	for _, credentials := range []bool{false, true} {
		t.Run(fmt.Sprintf("credentials=%v", credentials), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "devices.db")
			db, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			endpoint := "crash"
			if !credentials {
				if _, err = db.Create("other", store.Device{Endpoint: endpoint}); err != nil {
					t.Fatal(err)
				}
			}
			hash := sha256.Sum256([]byte("lwm_test_secret"))
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write\n", hash)))
			if err != nil {
				t.Fatal(err)
			}
			d := &crashingDevices{Devices: db, crash: true}
			srv := server.New(server.Config{Security: db.Security()})
			h := apiv1.New(apiv1.Options{Keys: keys, Devices: d, Server: srv})
			body := `{"endpoint":"crash"}`
			if credentials {
				body = `{"endpoint":"crash","credentials":{"psk":{"identity":"crash","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
			}
			post := func(h http.Handler) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/devices", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer lwm_test_secret")
				r.Header.Set("Idempotency-Key", "crash-key")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			func() { defer func() { _ = recover() }(); post(h) }()
			if d.status != 201 {
				t.Fatalf("initial status=%d body=%s", d.status, d.body)
			}
			_ = srv.Close()
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			srv = server.New(server.Config{Security: db.Security()})
			defer srv.Close()
			retry := post(apiv1.New(apiv1.Options{Keys: keys, Devices: db, Server: srv}))
			if retry.Code != d.status || retry.Body.String() != string(d.body) || retry.Header().Get("Location") != d.header.Get("Location") {
				t.Fatalf("retry: %d %s, want %d %s", retry.Code, retry.Body, d.status, d.body)
			}
			devices, _, err := db.List("default", store.DeviceFilter{}, "", 100)
			if err != nil || len(devices) != 1 {
				t.Fatalf("devices=%v err=%v", devices, err)
			}
			if !credentials {
				if _, err = db.Get("other", endpoint); err != nil {
					t.Fatalf("other tenant: %v", err)
				}
			} else if _, ok := db.Security().ByEndpoint(endpoint); !ok {
				t.Fatal("missing committed security")
			}
		})
	}
}
