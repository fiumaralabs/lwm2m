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
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestOperationGatewayPrefixContract(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	st := memory.NewOperations()
	var executor *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { executor.HandleEvent(ev) }})
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
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/25/0/0"), lwm2m.String("device"))
	c.Set(lwm2m.MustParsePath("/25/0/1"), lwm2m.String("d01"))
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if strings.HasPrefix(r.Path, "/d01/") {
			return codes.Changed, nil, nil, true
		}
		return 0, nil, nil, false
	})
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body, path string }{
		{"discover", `{"type":"discover","prefix":"d01","path":"/3/0"}`, "/d01/3/0"},
		{"write", `{"type":"write","prefix":"d01","path":"/3/0/9","values":[{"path":"/3/0/9","type":"integer","value":5}]}`, "/d01/3/0/9"},
		{"execute", `{"type":"execute","prefix":"d01","path":"/3/0/4"}`, "/d01/3/0/4"},
		{"delete", `{"type":"delete","prefix":"d01","path":"/3303/0"}`, "/d01/3303/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := deviceRequest(h, "POST", "/v1/devices/pump/operations", tc.body)
			if w.Code != 202 {
				t.Fatalf("POST: %d %s", w.Code, w.Body.String())
			}
			w = deviceRequest(h, "GET", w.Header().Get("Location")+"?wait=2", "")
			if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) {
				t.Fatalf("result: %d %s", w.Code, w.Body.String())
			}
			if req, _ := c.LastRequest(); req.Path != tc.path {
				t.Fatalf("path = %q, want %q", req.Path, tc.path)
			}
		})
	}
}
