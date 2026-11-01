package apiv1

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestJobCancelImmediatelyCancelsQueuedOperation(t *testing.T) {
	ctx := context.Background()
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var ex *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { ex.HandleEvent(ev) }})
	defer srv.Close()
	ops := memory.NewOperations()
	ex = operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	listener := coap.New(srv)
	defer listener.Close()
	addr, err := listener.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runner := JobRunner{}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",jobs:write,", tenant: "default"}}}
	handler := New(Options{Keys: keys, Server: srv, Executor: ex, Jobs: jobs, JobRunner: &runner})
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", "job", "running")
	if len(tasks) != 1 || tasks[0].Operation == "" {
		t.Fatalf("tasks: %+v", tasks)
	}
	w := profileWriteRequest(handler, "POST", "/v1/jobs/job/cancel", "", "lwm_test_secret", "")
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	op, err := ops.Get(ctx, "default", tasks[0].Operation)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != operations.Cancelled {
		t.Fatalf("operation status = %s, want cancelled", op.Status)
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	jobAfter, _ := jobs.Get("default", "job")
	if jobAfter.Status != "cancelled" || jobAfter.Counts.Cancelled != 1 || jobAfter.Counts.Running != 0 {
		t.Fatalf("job after cancellation: %+v", jobAfter)
	}
	op, err = ops.Get(ctx, "default", tasks[0].Operation)
	if err != nil || op.Status != operations.Cancelled {
		t.Fatalf("operation after tick: %+v, %v", op, err)
	}
	client := testclient.New(testclient.Config{Endpoint: "dev"})
	defer client.Close()
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	wakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := client.Register(wakeCtx); err != nil {
		t.Fatal(err)
	}
	control := operations.New("default", "dev", "execute", []byte(`{"path":"/3/0/4"}`), nil, time.Now())
	control, err = ex.Submit(wakeCtx, "default", control)
	if err != nil {
		t.Fatal(err)
	}
	control, err = ex.Wait(wakeCtx, "default", control.ID)
	if err != nil || control.Status != operations.Succeeded {
		t.Fatalf("control operation: %+v, %v", control, err)
	}
	if requests := client.Requests(); len(requests) != 1 {
		t.Fatalf("expected only the control request, got %+v", requests)
	}
}
