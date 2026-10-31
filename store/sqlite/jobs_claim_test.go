package sqlite_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestJobClaimsMemory(t *testing.T) { storetest.JobClaims(t, apiv1.NewMemoryJobStore()) }
func TestJobClaimsIndependentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"a"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
	if err := a.Jobs().Create("a", j); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.Jobs().StartJob("a", j.ID, []string{"a", "b"}, time.Now()); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	var wg sync.WaitGroup
	results := make(chan []apiv1.JobTask, 2)
	errs := make(chan error, 2)
	for _, jobs := range []*sqlite.Jobs{a.Jobs(), b.Jobs()} {
		wg.Add(1)
		go func() { defer wg.Done(); tasks, err := jobs.ClaimTasks("a", j.ID); results <- tasks; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	total := 0
	for tasks := range results {
		total += len(tasks)
	}
	if total != 1 {
		t.Fatalf("claimed %d tasks", total)
	}
}

func TestJobClaimsSQLite(t *testing.T) {
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	storetest.JobClaims(t, d.Jobs())
}
