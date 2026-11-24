package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestRecoverOrphanedJobWithCompletedAndQueuedOperations(t *testing.T) {
	t.Run("different steps", func(t *testing.T) { testRecoverySteps(t, false, false) })
	t.Run("two current operations fail closed", func(t *testing.T) { testRecoverySteps(t, true, false) })
	t.Run("terminal current operation", func(t *testing.T) { testRecoverySteps(t, false, true) })
}

func testRecoverySteps(t *testing.T, duplicate, terminal bool) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	job := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := db.Jobs().Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.Jobs().StartJob("tenant", job.ID, []string{"device"}, time.Now()); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	if !db.Jobs().PutTask("tenant", job.ID, apiv1.JobTask{Device: "device", Status: "pending", Step: 1}) {
		t.Fatal("put task")
	}
	if tasks, err := db.Jobs().ClaimTasks("tenant", job.ID); err != nil || len(tasks) != 1 {
		t.Fatalf("claim: %v %v", tasks, err)
	}
	first := operations.New("tenant", "device", "read", []byte(`{}`), nil, time.Now())
	first.Job = &job.ID
	if err := db.Operations().Create(ctx, "tenant", first); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ from, to operations.Status }{{operations.Queued, operations.Sent}, {operations.Sent, operations.Succeeded}} {
		if err := db.Operations().Update(ctx, "tenant", first.ID, change.from, operations.Change{Status: change.to, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	second := operations.New("tenant", "device", "read", []byte(`{}`), nil, time.Now())
	second.Job = &job.ID
	step := 1
	second.JobStep = &step
	if err := db.Operations().Create(ctx, "tenant", second); err != nil {
		t.Fatal(err)
	}
	if terminal {
		if err := db.Operations().Update(ctx, "tenant", second.ID, operations.Queued, operations.Change{Status: operations.Expired, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if duplicate {
		third := operations.New("tenant", "device", "read", []byte(`{}`), nil, time.Now())
		third.Job, third.JobStep = &job.ID, &step
		if err := db.Operations().Create(ctx, "tenant", third); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Jobs().RecoverJobClaims(ctx)
	if duplicate {
		if err == nil || !strings.Contains(err.Error(), `job "job"`) || !strings.Contains(err.Error(), `device "device"`) || !strings.Contains(err.Error(), "step 1") {
			t.Fatalf("expected job, device and step in ambiguity error, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	tasks := db.Jobs().ListTasks("tenant", job.ID, "")
	if len(tasks) != 1 || tasks[0].Status != "running" || tasks[0].Operation != second.ID || tasks[0].Step != 1 {
		t.Fatalf("recovered: %+v", tasks)
	}
}

// Recovery must only attach an operation belonging to the persisted step.
func TestRecoverOrphanedJobStepAfterReopen(t *testing.T) {
	for _, withOperations := range []bool{false, true} {
		name := "no current operation"
		if withOperations {
			name = "three mixed operations"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "jobs.db")
			db, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			job := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
			if err = db.Jobs().Create("tenant", job); err != nil {
				t.Fatal(err)
			}
			if ok, err := db.Jobs().StartJob("tenant", job.ID, []string{"device"}, time.Now()); !ok || err != nil {
				t.Fatalf("start: %v %v", ok, err)
			}
			if !db.Jobs().PutTask("tenant", job.ID, apiv1.JobTask{Device: "device", Status: "pending", Step: 2}) {
				t.Fatal("put task")
			}
			if tasks, err := db.Jobs().ClaimTasks("tenant", job.ID); err != nil || len(tasks) != 1 {
				t.Fatalf("claim: %v %v", tasks, err)
			}
			current := ""
			if withOperations {
				for step := 0; step < 3; step++ {
					op := operations.New("tenant", "device", "read", []byte(`{}`), nil, time.Now())
					op.Job, op.JobStep = &job.ID, &step
					if err := db.Operations().Create(ctx, "tenant", op); err != nil {
						t.Fatal(err)
					}
					if step == 0 {
						if err := db.Operations().Update(ctx, "tenant", op.ID, operations.Queued, operations.Change{Status: operations.Expired, At: time.Now()}); err != nil {
							t.Fatal(err)
						}
					}
					if step == 2 {
						current = op.ID
					}
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			wantStatus := "pending"
			if withOperations {
				wantStatus = "running"
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := db.Jobs().RecoverJobClaims(ctx); err != nil {
					t.Fatal(err)
				}
				tasks := db.Jobs().ListTasks("tenant", job.ID, "")
				if len(tasks) != 1 || tasks[0].Status != wantStatus || tasks[0].Operation != current || tasks[0].Step != 2 {
					t.Fatalf("recovery %d: %+v", attempt+1, tasks)
				}
			}
		})
	}
}
