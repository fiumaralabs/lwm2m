package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestDaemonGatewayRoutes(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var devices store.Devices = &memory.Devices{}
			if backend == "sqlite" {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "gateway.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				devices = db
			}
			if _, err := devices.Create("default", store.Device{Endpoint: "gw"}); err != nil {
				t.Fatal(err)
			}
			keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x firmware:write,devices:read", sha256.Sum256([]byte("lwm_test_secret")))))
			if err != nil {
				t.Fatal(err)
			}
			srv, h, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, devices)
			t.Cleanup(func() { _ = dispatcher.Close(); _ = executor.Close(); _ = srv.Close() })
			cb := coap.New(srv)
			t.Cleanup(func() { _ = cb.Close() })
			addr, err := cb.ListenUDP("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			c := testclient.New(testclient.Config{Endpoint: "gw"})
			t.Cleanup(func() { _ = c.Close() })
			c.Set(lwm2m.NewPath(25, 0, 0), lwm2m.String("sensor"))
			c.Set(lwm2m.NewPath(25, 0, 1), lwm2m.String("d01"))
			if err := c.Dial(addr.String()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if r, err := c.Register(ctx); err != nil || r.Code != codes.Created {
				t.Fatalf("register: %v %v", r, err)
			}
			req := httptest.NewRequest("GET", "/v1/devices/gw/end-devices", nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer lwm_test_secret")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"prefix":"d01"`) || !strings.Contains(w.Body.String(), `"id":"sensor"`) {
				t.Fatalf("gateway: %d %s", w.Code, w.Body.String())
			}
			req = httptest.NewRequest("POST", "/v1/devices/gw/firmware-updates", strings.NewReader(`{"uri":"coaps://example/fw"}`))
			req.Header.Set("Authorization", "Bearer lwm_test_secret")
			w = httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 201 {
				t.Fatalf("firmware update: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
