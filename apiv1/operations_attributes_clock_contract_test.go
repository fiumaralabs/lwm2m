package apiv1

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestOperationAttributesAndClockContract(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	st := memory.NewOperations()
	var executor *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { executor.HandleEvent(ev) }})
	defer srv.Close()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	executor = operations.NewExecutor(srv, st, operations.ExecutorOptions{Now: func() time.Time { return now }})
	defer executor.Close()
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Server: srv, Operations: st, Executor: executor})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
	c.Set(lwm2m.MustParsePath("/3/0/13"), lwm2m.Time(0))
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"type":"write-attributes","path":"/3/0/0","attributes":{"pmin":null,"pmax":20}}`, `{"type":"sync-clock"}`} {
		w := deviceRequest(h, "POST", "/v1/devices/pump/operations", body)
		if w.Code != 202 {
			t.Fatalf("POST %s: %d %s", body, w.Code, w.Body.String())
		}
		w = deviceRequest(h, "GET", w.Header().Get("Location")+"?wait=2", "")
		if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) {
			t.Fatalf("result %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"type":"write-attributes","path":"/","attributes":{"pmin":1}}`, `{"type":"write-attributes","path":"/3/0/0","attributes":{"pmin":"1"}}`, `{"type":"write-attributes","path":"/3/0/0","attributes":{"lt":5,"gt":2}}`, `{"type":"sync-clock","path":"/3/0/13"}`} {
		w := deviceRequest(h, "POST", "/v1/devices/pump/operations", body)
		if w.Code != 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
			t.Errorf("invalid %s: %d %s %s", body, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
}
