package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestCancelledJobReconcilesSubmittedOperation(t *testing.T) {
	ctx := context.Background()
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ops := memory.NewOperations()
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Operations: ops, Executor: jobSubmitterFunc(func(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
		return op, ops.Create(ctx, tenant, op)
	})}
	if err := tickUnrestricted(runner, ctx); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "running")[0]
	if !jobs.UpdateStatus("tenant", "job", "cancelled") {
		t.Fatal("cancel failed")
	}
	if err := ops.Update(ctx, "tenant", task.Operation, operations.Queued, operations.Change{Status: operations.Sent}); err != nil {
		t.Fatal(err)
	}
	if err := ops.Update(ctx, "tenant", task.Operation, operations.Sent, operations.Change{Status: operations.Succeeded}); err != nil {
		t.Fatal(err)
	}
	if err := tickUnrestricted(runner, ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := jobs.Get("tenant", "job")
	if j.Status != "cancelled" || j.Counts.Succeeded != 1 || j.Counts.Running != 0 || j.Counts.Pending != 1 {
		t.Fatalf("job: %+v tasks: %+v", j, jobs.ListTasks("tenant", "job", ""))
	}
}
