package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestJobRunnerDoesNotDeliverAnotherTenantsJob(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"execute","path":"/3/0/4"}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("other", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("other", "job", []string{"dev"}, time.Now()); err != nil {
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
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	runner := JobRunner{Jobs: jobs, Operations: ops, Executor: ex}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("other", "job", "")[0]
	if task.Operation != "" {
		t.Fatalf("cross-tenant operation dispatched to registered endpoint: %+v", task)
	}
	items, _, err := ops.List(ctx, "other", operations.ListFilter{}, "", 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("cross-tenant operations=%+v err=%v", items, err)
	}
}
