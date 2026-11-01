package apiv1

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobCancelKeepsInFlightOperation(t *testing.T) {
	ctx := context.Background()
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := memory.NewOperations()
	ex := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	runner := JobRunner{}
	keys := Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",jobs:write,", tenant: "default"}}}
	handler := New(Options{Keys: keys, Server: srv, Executor: ex, Jobs: jobs, JobRunner: &runner})
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("default", "job", "running")[0]
	if err := ops.Update(ctx, "default", task.Operation, operations.Queued, operations.Change{Status: operations.Sent}); err != nil {
		t.Fatal(err)
	}
	w := profileWriteRequest(handler, "POST", "/v1/jobs/job/cancel", "", "lwm_test_secret", "")
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	op, err := ops.Get(ctx, "default", task.Operation)
	if err != nil || op.Status != operations.Sent {
		t.Fatalf("operation %+v, %v", op, err)
	}
	if err := ops.Update(ctx, "default", task.Operation, operations.Sent, operations.Change{Status: operations.Succeeded}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := jobs.Get("default", "job")
	if got.Status != "cancelled" || got.Counts.Succeeded != 1 || got.Counts.Running != 0 {
		t.Fatalf("job %+v", got)
	}
}

func TestJobCancelCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("default", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("default", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{})
	defer srv.Close()
	ops := memory.NewOperations()
	ex := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	runner := JobRunner{}
	keys := Keys{entries: map[string]credential{"other": {hash: sha256.Sum256([]byte("lwm_other_secret")), scopes: ",jobs:write,", tenant: "other"}}}
	handler := New(Options{Keys: keys, Server: srv, Executor: ex, Jobs: jobs, JobRunner: &runner})
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("default", "job", "running")[0]
	w := profileWriteRequest(handler, "POST", "/v1/jobs/job/cancel", "", "lwm_other_secret", "")
	if w.Code != 404 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	op, err := ops.Get(ctx, "default", task.Operation)
	if err != nil || op.Status != operations.Queued {
		t.Fatalf("operation %+v %v", op, err)
	}
	got, _ := jobs.Get("default", "job")
	if got.Status != "running" {
		t.Fatalf("job %+v", got)
	}
}
