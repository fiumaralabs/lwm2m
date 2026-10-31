package sqlite

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestSQLiteSharedJobReplays(t *testing.T) {
	storetest.JobReplays(t, func(t *testing.T) (apiv1.JobStore, apiv1.JobReplayStore, func(string, string, time.Duration)) {
		t.Helper()
		d, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		jobs := d.Jobs()
		replay := any(jobs).(apiv1.JobReplayStore)
		return jobs, replay, func(tenant, key string, age time.Duration) {
			t.Helper()
			if _, err := d.db.Exec("UPDATE job_replays SET expires = expires - ? WHERE tenant = ? AND key = ?", int64(age), tenant, key); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func replayJob(id string) apiv1.JobCreateResult {
	j := apiv1.Job{ID: id, Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: json.RawMessage(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
	return apiv1.JobCreateResult{Job: &j}
}

func TestSQLiteJobReplaySeparateInterface(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, ok := any(d.Jobs()).(apiv1.JobReplayStore)
	if !ok {
		t.Fatal("SQLite jobs must implement JobReplayStore")
	}
	first := apiv1.JobCreateResult{Problem: &apiv1.Problem{Title: "first"}}
	got, err := s.CreateResult("tenant", "key", first)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("create: %#v %v", got, err)
	}
	got, found := s.ReplayCreate("tenant", "key")
	if !found || !reflect.DeepEqual(got, first) {
		t.Fatalf("replay: %#v %v", got, found)
	}
}

func TestSQLiteJobReplayConcurrentReopenAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := d.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 22 {
		t.Fatalf("migration version %d: %v", version, err)
	}
	stores := []any{d.Jobs(), d2.Jobs()}
	ss := make([]apiv1.JobReplayStore, 2)
	for i := range stores {
		var ok bool
		ss[i], ok = stores[i].(apiv1.JobReplayStore)
		if !ok {
			t.Fatal("missing replay interface")
		}
	}
	const n = 32
	results := make([]apiv1.JobCreateResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = ss[i%2].CreateResult("tenant", "key", replayJob(fmt.Sprintf("job-%d", i)))
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range results {
		if errs[i] != nil || !reflect.DeepEqual(results[0], results[i]) {
			t.Fatalf("result %d: %#v %v, winner %#v", i, results[i], errs[i], results[0])
		}
	}
	if jobs := d.Jobs().List("tenant"); len(jobs) != 1 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, ok := any(reopened.Jobs()).(apiv1.JobReplayStore).ReplayCreate("tenant", "key")
	if !ok || !reflect.DeepEqual(got, results[0]) {
		t.Fatalf("reopen replay: %#v %v", got, ok)
	}
}

func TestSQLiteJobReplayAtomicRollback(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.db.Exec(`CREATE TRIGGER fail_job_replay BEFORE INSERT ON job_replays BEGIN SELECT RAISE(FAIL, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	s := any(d.Jobs()).(apiv1.JobReplayStore)
	if _, err = s.CreateResult("tenant", "key", replayJob("rollback")); err == nil {
		t.Fatal("expected replay insert failure")
	}
	if _, ok := d.Jobs().Get("tenant", "rollback"); ok {
		t.Fatal("job survived failed replay insert")
	}
	if _, ok := s.ReplayCreate("tenant", "key"); ok {
		t.Fatal("replay survived failed insert")
	}
}

func TestSQLiteJobReplayUpgradeFrom19(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Jobs().Create("tenant", *replayJob("prior").Job); err != nil {
		t.Fatal(err)
	}
	if _, err = d.db.Exec(`DROP TABLE job_replays; PRAGMA user_version=19`); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var version int
	if err = d.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 22 {
		t.Fatalf("upgrade version %d: %v", version, err)
	}
	if _, ok := d.Jobs().Get("tenant", "prior"); !ok {
		t.Fatal("upgrade lost existing job")
	}
	s := any(d.Jobs()).(apiv1.JobReplayStore)
	problem := apiv1.JobCreateResult{Problem: &apiv1.Problem{Title: "stored"}}
	if _, err = s.CreateResult("tenant", "key", problem); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.ReplayCreate("tenant", "key"); !ok || !reflect.DeepEqual(got, problem) {
		t.Fatalf("upgrade replay: %#v %v", got, ok)
	}
}

func TestSQLiteJobReplayExpiryAndEmptyKey(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, ok := any(d.Jobs()).(apiv1.JobReplayStore)
	if !ok {
		t.Fatal("missing replay interface")
	}
	before := time.Now()
	first, err := s.CreateResult("tenant", "key", replayJob("first"))
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	var expiry int64
	if err := d.db.QueryRow("SELECT expires FROM job_replays WHERE tenant=? AND key=?", "tenant", "key").Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if expiry < before.Add(24*time.Hour).UnixNano() || expiry > after.Add(24*time.Hour).UnixNano() {
		t.Fatalf("wrong expiry: %v", time.Unix(0, expiry))
	}
	if _, ok := s.ReplayCreate("other", "key"); ok {
		t.Fatal("tenant leaked")
	}
	got, err := s.CreateResult("tenant", "key", replayJob("ignored"))
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("first wins: %#v %v", got, err)
	}
	if _, err = d.db.Exec("UPDATE job_replays SET expires=? WHERE tenant=? AND key=?", time.Now().Add(-time.Second).UnixNano(), "tenant", "key"); err != nil {
		t.Fatal(err)
	}
	if _, ok = s.ReplayCreate("tenant", "key"); ok {
		t.Fatal("expired replay")
	}
	if _, err = s.CreateResult("tenant", "key", replayJob("second")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateResult("tenant", "", replayJob("plain")); err != nil {
		t.Fatal(err)
	}
	if _, ok = s.ReplayCreate("tenant", ""); ok {
		t.Fatal("empty key replayed")
	}
	var count int
	if err = d.db.QueryRow("SELECT count(*) FROM job_replays WHERE key='' ").Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty key count %d: %v", count, err)
	}
}
