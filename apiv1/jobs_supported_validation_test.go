package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerInvalidSupportedNewOperations(t *testing.T) {
	for _, body := range []string{`{"type":"create","path":"/3"}`, `{"type":"discover","path":"/3","depth":4}`} {
		t.Run(body, func(t *testing.T) {
			jobs := NewMemoryJobStore()
			job := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(body)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
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
			if err := tickUnrestricted(JobRunner{Jobs: jobs, Executor: ex, Operations: ops}, context.Background()); err != nil {
				t.Fatal(err)
			}
			tasks := jobs.ListTasks("default", "job", "")
			if len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].Error != operations.ErrInvalid.Error() {
				t.Fatalf("tasks=%+v", tasks)
			}
		})
	}
}
