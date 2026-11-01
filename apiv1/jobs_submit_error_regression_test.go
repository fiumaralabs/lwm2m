package apiv1

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type failPendingOnce struct {
	operations.Operations
	failed atomic.Bool
}

func (s *failPendingOnce) Pending(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	if s.failed.CompareAndSwap(false, true) {
		return nil, errors.New("transient pending failure")
	}
	return s.Operations.Pending(ctx, tenant, device)
}

func TestJobRunnerRetainsPersistedOperationAfterSubmitError(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ops := &failPendingOnce{Operations: memory.NewOperations()}
	srv := server.New(server.Config{})
	defer srv.Close()
	ex := operations.NewExecutor(srv, ops, operations.ExecutorOptions{})
	defer ex.Close()
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Operations: ops, Executor: ex}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	if task.Status != "running" || task.Operation == "" {
		t.Fatalf("persisted operation lost: %+v", task)
	}
	op, err := ops.Get(context.Background(), "tenant", task.Operation)
	if err != nil || op.Status != operations.Queued {
		t.Fatalf("operation=%+v error=%v", op, err)
	}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := jobs.Get("tenant", "job")
	if j.Status != "running" || j.Counts.Running != 1 {
		t.Fatalf("job completed prematurely: %+v", j)
	}
}
