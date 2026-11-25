package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerUnsupportedRealExecutor(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{"type":"read-composite","paths":["/3/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
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
	runner := JobRunner{Jobs: jobs, Executor: ex, Operations: ops}
	if err := tickUnrestricted(runner, context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := jobs.ListTasks("default", "job", "")
	if len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].Error != "unsupported_operation" || tasks[0].Operation != "" {
		t.Fatalf("tasks=%+v", tasks)
	}
}
