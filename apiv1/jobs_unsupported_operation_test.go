package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
)

func TestJobRunnerRejectsUnsupportedOperationPerTarget(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{"type":"read-composite","paths":["/3/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 2}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(context.Context, string, operations.Operation) (operations.Operation, error) {
		calls++
		return operations.Operation{}, operations.ErrInvalid
	})}
	if err := tickUnrestricted(runner, context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("tenant", "job", "")
	if len(tasks) != 2 || calls != 2 {
		t.Fatalf("tasks=%+v submits=%d", tasks, calls)
	}
	for _, task := range tasks {
		if task.Status != "failed" || task.Error != "unsupported_operation" {
			t.Errorf("task=%+v", task)
		}
	}
}

func TestJobRunnerInvalidSupportedOperationKeepsError(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read","paths":[]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("tenant", "job", []string{"dev"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	runner := JobRunner{ServerTenant: "tenant", Jobs: jobs, Executor: jobSubmitterFunc(func(context.Context, string, operations.Operation) (operations.Operation, error) {
		return operations.Operation{}, operations.ErrInvalid
	})}
	if err := tickUnrestricted(runner, context.Background()); err != nil {
		t.Fatal(err)
	}
	task := jobs.ListTasks("tenant", "job", "")[0]
	if task.Status != "failed" || task.Error != operations.ErrInvalid.Error() {
		t.Fatalf("task=%+v", task)
	}
}
