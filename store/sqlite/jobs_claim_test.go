package sqlite_test

import (
	"database/sql"
	"fmt"
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

// A claim holds a write reservation before reading. Status changes must wait
// for it instead of reading under a deferred transaction and failing to upgrade.
func TestJobStatusWaitsForClaimWriter(t *testing.T) {
	for _, status := range []string{"paused", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "jobs.db")
			d, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			j := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"a"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
			if err := d.Jobs().Create("a", j); err != nil {
				t.Fatal(err)
			}
			if ok, err := d.Jobs().StartJob("a", j.ID, []string{"a"}, time.Now()); !ok || err != nil {
				t.Fatalf("start: %v %v", ok, err)
			}
			lock, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			lock.SetMaxOpenConns(1)
			if _, err := lock.Exec("PRAGMA busy_timeout=5000"); err != nil {
				t.Fatal(err)
			}
			if _, err := lock.Exec("BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			defer lock.Exec("ROLLBACK")
			result := make(chan any, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						result <- p
					}
				}()
				result <- d.Jobs().UpdateStatus("a", j.ID, status)
			}()
			select {
			case r := <-result:
				t.Fatalf("status transition completed while writer held lock: %v", r)
			case <-time.After(100 * time.Millisecond):
			}
			if _, err := lock.Exec("COMMIT"); err != nil {
				t.Fatal(err)
			}
			select {
			case r := <-result:
				if r != true {
					t.Fatalf("transition: %v", r)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("transition did not finish")
			}
			tasks, err := d.Jobs().ClaimTasks("a", j.ID)
			if err != nil || len(tasks) != 0 {
				t.Fatalf("claim after %s: %v %v", status, tasks, err)
			}
		})
	}
}

func TestJobClaimConcurrentStatusIndependentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
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
	for _, status := range []string{"paused", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				j := apiv1.Job{ID: fmt.Sprintf("%s-%d", status, i), Target: apiv1.JobTarget{Devices: []string{"a"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
				if err := a.Jobs().Create("a", j); err != nil {
					t.Fatal(err)
				}
				if ok, err := a.Jobs().StartJob("a", j.ID, []string{"a"}, time.Now()); !ok || err != nil {
					t.Fatalf("start: %v %v", ok, err)
				}
				start := make(chan struct{})
				results := make(chan error, 2)
				go func() { <-start; _, err := a.Jobs().ClaimTasks("a", j.ID); results <- err }()
				go func() {
					defer func() {
						if p := recover(); p != nil {
							results <- fmt.Errorf("transition panic: %v", p)
						}
					}()
					<-start
					if !b.Jobs().UpdateStatus("a", j.ID, status) {
						results <- fmt.Errorf("transition rejected")
						return
					}
					results <- nil
				}()
				close(start)
				for k := 0; k < 2; k++ {
					if err := <-results; err != nil {
						t.Fatal(err)
					}
				}
				claimed, err := a.Jobs().ClaimTasks("a", j.ID)
				if err != nil || len(claimed) != 0 {
					t.Fatalf("claim after %s: %v %v", status, claimed, err)
				}
			}
		})
	}
}
