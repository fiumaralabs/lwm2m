package apiv1

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestDiscoverDepthAPIDelivery(t *testing.T) {
	for _, depth := range []string{"", `,"depth":0`, `,"depth":3`} {
		t.Run(fmt.Sprintf("depth%s", depth), func(t *testing.T) {
			keys := deviceKeys()
			e := keys.entries["test"]
			e.scopes += "operations:write,"
			keys.entries["test"] = e
			ops := memory.NewOperations()
			var ex *operations.Executor
			srv := server.New(server.Config{OnEvent: func(ev server.Event) { ex.HandleEvent(ev) }})
			defer srv.Close()
			ex = operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
			defer ex.Close()
			transport := coap.New(srv)
			defer transport.Close()
			addr, err := transport.ListenUDP("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			h := New(Options{Keys: keys, Server: srv, Operations: ops, Executor: ex})
			deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
			client := testclient.New(testclient.Config{Endpoint: "pump"})
			defer client.Close()
			client.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("test"))
			if err := client.Dial(addr.String()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Register(ctx); err != nil {
				t.Fatal(err)
			}
			w := deviceRequest(h, "POST", "/v1/devices/pump/operations?wait=2", `{"type":"discover","path":"/3/0"`+depth+`}`)
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var op operations.Operation
			if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || op.Status != operations.Succeeded {
				t.Fatalf("op=%+v err=%v", op, err)
			}
			want := []string(nil)
			if depth != "" {
				if depth == `,"depth":0` {
					want = []string{"depth=0"}
				} else {
					want = []string{"depth=3"}
				}
			}
			req := client.Requests()
			if len(req) != 1 || !slices.Equal(req[0].Queries, want) {
				t.Fatalf("requests=%+v want query=%v", req, want)
			}
		})
	}
}

func TestDiscoverDepthJobDelivery(t *testing.T) {
	jobs := NewMemoryJobStore()
	body := `{"name":"discover","target":{"devices":["dev"]},"action":{"operation":{"type":"discover","path":"/3/0","depth":0}},"rollout":{"maxConcurrent":1},"ttlSeconds":60}`
	w := jobRequest(NewJobsHandler(jobs), "POST", "/v1/jobs", body, "tenant", "jobs:write")
	if w.Code != 201 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", job.ID, []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ops := memory.NewOperations()
	var ex *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { ex.HandleEvent(ev) }})
	defer srv.Close()
	ex = operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	transport := coap.New(srv)
	defer transport.Close()
	addr, err := transport.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := testclient.New(testclient.Config{Endpoint: "dev"})
	defer client.Close()
	client.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("test"))
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Operations: ops, Executor: ex}
	if err := tickUnrestricted(runner, ctx); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("tenant", job.ID, "")
	if len(tasks) != 1 || tasks[0].Operation == "" {
		t.Fatalf("tasks=%+v", tasks)
	}
	op, err := ex.Wait(ctx, "tenant", tasks[0].Operation)
	if err != nil || op.Status != operations.Succeeded {
		t.Fatalf("op=%+v err=%v", op, err)
	}
	req := client.Requests()
	if len(req) != 1 || !slices.Equal(req[0].Queries, []string{"depth=0"}) {
		t.Fatalf("requests=%+v", req)
	}
}
