package sqlite_test

import (
	"database/sql"
	"fmt"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"path/filepath"
	"sync"
	"testing"
)

func TestJobReplaySurvivesRestartReview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := apiv1.Job{ID: "one", Target: apiv1.JobTarget{Devices: []string{"d"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
	result, err := db.Jobs().CreateJobResult("a", "key", apiv1.JobCreateResult{Job: &first})
	if err != nil || result.Job.ID != "one" {
		t.Fatalf("%+v %v", result, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	second := first
	second.ID = "two"
	result, err = db.Jobs().CreateJobResult("a", "key", apiv1.JobCreateResult{Job: &second})
	if err != nil || result.Job == nil || result.Job.ID != "one" || len(db.Jobs().List("a")) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestJobReplayMultipleSQLiteConnectionsReview(t *testing.T) {
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
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := a
			if i%2 == 0 {
				d = b
			}
			j := apiv1.Job{ID: fmt.Sprint(i), Target: apiv1.JobTarget{Devices: []string{"d"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
			r, e := d.Jobs().CreateJobResult("a", "key", apiv1.JobCreateResult{Job: &j})
			errs <- e
			if e == nil {
				ids <- r.Job.ID
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for v := range ids {
		if id == "" {
			id = v
		}
		if v != id {
			t.Fatalf("different ids: %s %s", id, v)
		}
	}
	if len(a.Jobs().List("a")) != 1 {
		t.Fatal("duplicate jobs")
	}
	raw, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	if _, e = raw.Exec(`UPDATE job_replays SET expires=0`); e != nil {
		t.Fatal(e)
	}
	j := apiv1.Job{ID: "new", Target: apiv1.JobTarget{Devices: []string{"d"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
	r, e := a.Jobs().CreateJobResult("a", "key", apiv1.JobCreateResult{Job: &j})
	if e != nil || r.Job.ID != "new" {
		t.Fatalf("expiry: %+v %v", r, e)
	}
	r, e = a.Jobs().CreateJobResult("b", "key", apiv1.JobCreateResult{Job: &j})
	if e != nil || r.Job.ID != "new" || len(a.Jobs().List("b")) != 1 {
		t.Fatalf("tenant: %+v %v", r, e)
	}
}
