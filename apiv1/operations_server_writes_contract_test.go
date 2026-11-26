package apiv1

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestOperationServerWritesHTTPDelivery(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	st := memory.NewOperations()
	var executor *operations.Executor
	srv := server.New(server.Config{ShortServerID: 101, OnEvent: func(ev server.Event) { executor.HandleEvent(ev) }})
	defer srv.Close()
	executor = operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	defer executor.Close()
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Server: srv, Operations: st, Executor: executor})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatalf("device: %d %s", w.Code, w.Body.String())
	}
	c := testclient.New(testclient.Config{Endpoint: "pump", Version: "1.2"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(5))
	c.Set(lwm2m.MustParsePath("/1/3/0"), lwm2m.Integer(101))
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body, path string
		value      lwm2m.Value
	}{
		{`{"type":"set-lifetime","lifetimeSeconds":300}`, "/1/3/1", lwm2m.Integer(300)},
		{`{"type":"set-binding","binding":"UQ"}`, "/1/3/7", lwm2m.String("UQ")},
		{`{"type":"set-preferred-transport","binding":"U"}`, "/1/3/22", lwm2m.String("U")},
		{`{"type":"set-profile-hash-algorithm","suite":0}`, "/1/3/27", lwm2m.Unsigned(0)},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := deviceRequest(h, "POST", "/v1/devices/pump/operations", tc.body)
			if w.Code != 202 {
				t.Fatalf("POST %s: %d %s", tc.body, w.Code, w.Body.String())
			}
			w = deviceRequest(h, "GET", w.Header().Get("Location")+"?wait=2", "")
			if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) {
				t.Fatalf("result: %d %s", w.Code, w.Body.String())
			}
			got, ok := c.Get(lwm2m.MustParsePath(tc.path))
			if !ok || !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("got %+v, %v; want %+v", got, ok, tc.value)
			}
		})
	}
}
