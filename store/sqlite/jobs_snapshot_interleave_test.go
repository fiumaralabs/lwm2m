package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type startInterleaver struct {
	apiv1.JobStore
	devices store.Devices
	t       *testing.T
}

func (s startInterleaver) mutate() {
	s.t.Helper()
	if err := s.devices.Delete("tenant", "old"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.devices.Create("tenant", store.Device{Endpoint: "new", Labels: map[string]string{"env": "prod"}}); err != nil {
		s.t.Fatal(err)
	}
}
func (s startInterleaver) StartJob(tenant, id string, devices []string, now time.Time) (bool, error) {
	s.mutate()
	return s.JobStore.StartJob(tenant, id, devices, now)
}
func (s startInterleaver) StartJobSnapshot(ctx context.Context, devices store.Devices, tenant, id string, now time.Time) (bool, error) {
	s.mutate()
	return s.JobStore.(interface {
		StartJobSnapshot(context.Context, store.Devices, string, string, time.Time) (bool, error)
	}).StartJobSnapshot(ctx, devices, tenant, id, now)
}
func TestRunnerSnapshotMutationBeforeStart(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var devices store.Devices
			var jobs apiv1.JobStore
			if kind == "memory" {
				devices = &memory.Devices{}
				jobs = apiv1.NewMemoryJobStore()
			} else {
				d, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				devices = d
				jobs = d.Jobs()
			}
			if _, err := devices.Create("tenant", store.Device{Endpoint: "old", Labels: map[string]string{"env": "prod"}}); err != nil {
				t.Fatal(err)
			}
			if err := jobs.Create("tenant", apiv1.Job{ID: "job", Target: apiv1.JobTarget{Selector: "env=prod"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}); err != nil {
				t.Fatal(err)
			}
			r := apiv1.JobRunner{Jobs: startInterleaver{JobStore: jobs, devices: devices, t: t}, Devices: devices}
			if err := r.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			tasks := jobs.ListTasks("tenant", "job", "")
			if len(tasks) != 1 || tasks[0].Device != "new" {
				t.Fatalf("want new after mutation, got %+v", tasks)
			}
		})
	}
}

func TestRunnerSnapshotAcrossMoreThanOnePage(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var devices store.Devices
			var jobs apiv1.JobStore
			if kind == "memory" {
				devices = &memory.Devices{}
				jobs = apiv1.NewMemoryJobStore()
			} else {
				d, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				devices = d
				jobs = d.Jobs()
			}
			for i := 0; i < 501; i++ {
				if _, err := devices.Create("tenant", store.Device{Endpoint: fmt.Sprintf("d%03d", i), Labels: map[string]string{"env": "prod"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := jobs.Create("tenant", apiv1.Job{ID: "job", Target: apiv1.JobTarget{Selector: "env=prod"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}); err != nil {
				t.Fatal(err)
			}
			if err := (apiv1.JobRunner{Jobs: jobs, Devices: devices}).Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			tasks := jobs.ListTasks("tenant", "job", "")
			if len(tasks) != 501 {
				t.Fatalf("got %d tasks, want 501", len(tasks))
			}
		})
	}
}

func TestRunnerRejectsDifferentSQLiteStores(t *testing.T) {
	a, err := sqlite.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	jobs := a.Jobs()
	if err = jobs.Create("tenant", apiv1.Job{ID: "job", Target: apiv1.JobTarget{Selector: "env=prod"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	if err = (apiv1.JobRunner{Jobs: jobs, Devices: b}).Tick(context.Background()); err == nil {
		t.Fatal("expected mismatched stores to fail")
	}
	j, _ := jobs.Get("tenant", "job")
	if j.Status != "scheduled" || len(jobs.ListTasks("tenant", "job", "")) != 0 {
		t.Fatal("failed start modified job")
	}
}
