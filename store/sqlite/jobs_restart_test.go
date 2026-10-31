package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJobsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := d.Jobs()
	j := apiv1.Job{ID: "one", Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
	if err = s.Create("a", j); err != nil {
		t.Fatal(err)
	}
	if !s.PutTask("a", "one", apiv1.JobTask{Device: "device", Status: "pending"}) || !s.UpdateStatus("a", "one", "running") {
		t.Fatal("setup")
	}
	want, ok := s.Get("a", "one")
	if !ok {
		t.Fatal("missing")
	}
	tasks := s.ListTasks("a", "one", "")
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, ok := d.Jobs().Get("a", "one")
	if !ok || !reflect.DeepEqual(want, got) || !reflect.DeepEqual(tasks, d.Jobs().ListTasks("a", "one", "")) {
		t.Fatalf("reopen got %+v, want %+v", got, want)
	}
}
