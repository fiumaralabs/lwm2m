package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestRecoverOrphanedJobClaims(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs := db.Jobs()
	job := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"a", "b"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 2}, TTLSeconds: 60}
	if err := jobs.Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if ok, err := jobs.StartJob("tenant", job.ID, []string{"a", "b"}, time.Now()); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	tasks, err := jobs.ClaimTasks("tenant", job.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("claims: %v %v", tasks, err)
	}
	op := operations.New("tenant", "b", "read", []byte(`{"paths":["/3/0/0"]}`), nil, time.Now())
	op.Job = &job.ID
	if err := db.Operations().Create(context.Background(), "tenant", op); err != nil {
		t.Fatal(err)
	}
	if err := db.Operations().Update(context.Background(), "tenant", op.ID, operations.Queued, operations.Change{Status: operations.Expired, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	recoverer, ok := any(jobs).(interface{ RecoverJobClaims(context.Context) error })
	if !ok {
		t.Fatal("job store does not recover orphaned claims")
	}
	for i := 0; i < 2; i++ {
		if err := recoverer.RecoverJobClaims(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	tasks = jobs.ListTasks("tenant", job.ID, "")
	if len(tasks) != 2 || tasks[0].Device != "a" || tasks[0].Status != "pending" || tasks[1].Device != "b" || tasks[1].Status != "running" || tasks[1].Operation != op.ID {
		t.Fatalf("recovered tasks: %+v", tasks)
	}
	runner := apiv1.JobRunner{Jobs: jobs, Operations: db.Operations()}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks = jobs.ListTasks("tenant", job.ID, "")
	if tasks[1].Status != "expired" || tasks[1].Operation != op.ID {
		t.Fatalf("task did not follow operation: %+v", tasks[1])
	}
	claimed, err := jobs.ClaimTasks("tenant", job.ID)
	if err != nil || len(claimed) != 1 || claimed[0].Device != "a" {
		t.Fatalf("reclaim: %+v %v", claimed, err)
	}
}
