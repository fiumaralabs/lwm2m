package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerStartsTenantSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := NewMemoryJobStore()
	devices := &memory.Devices{}
	for _, x := range []struct{ tenant, id string }{{"a", "one"}, {"b", "other"}} {
		if _, err := devices.Create(x.tenant, store.Device{Endpoint: x.id, Labels: map[string]string{"env": "prod"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		tenant, id string
		target     JobTarget
		start      *time.Time
	}{{"a", "explicit", JobTarget{Devices: []string{"one", "other", "one"}}, nil}, {"b", "selector", JobTarget{Selector: "env=prod"}, nil}, {"a", "later", JobTarget{Selector: "env=prod"}, func() *time.Time { v := now.Add(time.Hour); return &v }()}, {"a", "cancel", JobTarget{Selector: "env=prod"}, nil}} {
		j := Job{ID: x.id, Target: x.target, Action: JobAction{Operation: []byte(`{}`)}, Rollout: JobRollout{MaxConcurrent: 1, StartAt: x.start}, TTLSeconds: 1}
		if err := jobs.Create(x.tenant, j); err != nil {
			t.Fatal(err)
		}
	}
	if err := jobs.Create("a", Job{ID: "paused", Target: JobTarget{Selector: "env=prod"}, Action: JobAction{Operation: []byte(`{}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	if !jobs.UpdateStatus("a", "paused", "running") || !jobs.UpdateStatus("a", "paused", "paused") {
		t.Fatal("pause")
	}
	if !jobs.UpdateStatus("a", "cancel", "cancelled") {
		t.Fatal("cancel")
	}
	r := JobRunner{Jobs: jobs, Devices: devices, Clock: func() time.Time { return now }}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ tenant, id, device string }{{"a", "explicit", "one"}, {"b", "selector", "other"}} {
		j, _ := jobs.Get(x.tenant, x.id)
		tasks := jobs.ListTasks(x.tenant, x.id, "")
		if j.Status != "running" || j.StartedAt == nil || !j.StartedAt.Equal(now) || len(tasks) != 1 || tasks[0].Device != x.device || tasks[0].Status != "pending" {
			t.Fatalf("job=%+v tasks=%+v", j, tasks)
		}
	}
	for _, id := range []string{"later", "cancel", "paused"} {
		if len(jobs.ListTasks("a", id, "")) != 0 {
			t.Fatal(id)
		}
	}
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(jobs.ListTasks("a", "explicit", "")) != 1 {
		t.Fatal("snapshot repeated")
	}
}
