package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type sqliteSubmitter struct{ ops *sqlite.Operations }

func (s sqliteSubmitter) Submit(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
	return op, s.ops.Create(ctx, tenant, op)
}

func TestJobRunnerSQLiteTenantPauseResumeAndReconcile(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs, ops := db.Jobs(), db.Operations()
	for _, tenant := range []string{"a", "b"} {
		job := apiv1.Job{ID: "same", Target: apiv1.JobTarget{Devices: []string{tenant}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
		if err := jobs.Create(tenant, job); err != nil {
			t.Fatal(err)
		}
		if ok, err := jobs.StartJob(tenant, "same", []string{tenant}, time.Now()); !ok || err != nil {
			t.Fatalf("start %s: %v %v", tenant, ok, err)
		}
	}
	if !jobs.UpdateStatus("b", "same", "paused") {
		t.Fatal("pause")
	}
	runner := apiv1.JobRunner{Jobs: jobs, Operations: ops, Executor: sqliteSubmitter{ops}}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, b := jobs.ListTasks("a", "same", ""), jobs.ListTasks("b", "same", "")
	if len(a) != 1 || a[0].Device != "a" || a[0].Operation == "" || len(b) != 1 || b[0].Status != "pending" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if _, err := ops.Get(context.Background(), "b", a[0].Operation); err == nil {
		t.Fatal("operation visible across tenants")
	}
	if !jobs.UpdateStatus("b", "same", "running") {
		t.Fatal("resume")
	}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	b = jobs.ListTasks("b", "same", "")
	if b[0].Device != "b" || b[0].Operation == "" {
		t.Fatalf("b=%+v", b)
	}
	for _, tc := range []struct {
		tenant, id string
		status     operations.Status
	}{{"a", a[0].Operation, operations.Succeeded}, {"b", b[0].Operation, operations.Failed}} {
		if err := ops.Update(context.Background(), tc.tenant, tc.id, operations.Queued, operations.Change{Status: operations.Sent, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := ops.Update(context.Background(), tc.tenant, tc.id, operations.Sent, operations.Change{Status: tc.status, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := runner.Tick(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, tc := range []struct{ tenant, status string }{{"a", "succeeded"}, {"b", "completed_with_failures"}} {
		j, ok := jobs.Get(tc.tenant, "same")
		if !ok || j.Status != tc.status {
			t.Errorf("%s job=%+v", tc.tenant, j)
		}
	}
}
