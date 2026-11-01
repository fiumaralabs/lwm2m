package storetest

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func Jobs(t *testing.T, fresh func() apiv1.JobStore) {
	t.Helper()
	t.Run("AtomicStart", func(t *testing.T) {
		s := fresh()
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		j := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Selector: "x=y"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
		for _, tenant := range []string{"", "other"} {
			if err := s.Create(tenant, j); err != nil {
				t.Fatal(err)
			}
		}
		tenants, err := s.JobTenants()
		if err != nil || len(tenants) != 2 || tenants[0] != "" || tenants[1] != "other" {
			t.Fatalf("tenants %v: %v", tenants, err)
		}
		if ok, err := s.StartJob("", "job", []string{"a", "a"}, now); err == nil || ok {
			t.Fatal("duplicate accepted")
		}
		j, _ = s.Get("", "job")
		if j.Status != "scheduled" || len(s.ListTasks("", "job", "")) != 0 {
			t.Fatal("partial transaction")
		}
		var wg sync.WaitGroup
		results := make(chan bool, 2)
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := s.StartJob("", "job", []string{"a", "b"}, now)
				results <- ok
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		winners := 0
		for ok := range results {
			if ok {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("winners %d", winners)
		}
		j, _ = s.Get("", "job")
		if j.Status != "running" || j.StartedAt == nil || !j.StartedAt.Equal(now) || j.Counts.Pending != 2 {
			t.Fatalf("job %+v", j)
		}
		if j, _ := s.Get("other", "job"); j.Status != "scheduled" || j.Counts.Total != 0 {
			t.Fatal("tenant leak")
		}
		if !s.UpdateStatus("other", "job", "cancelled") {
			t.Fatal("cancel")
		}
		if ok, err := s.StartJob("other", "job", []string{"a"}, now); ok || err != nil {
			t.Fatal("started cancelled job")
		}
	})
	t.Run("Transitions", func(t *testing.T) {
		for _, tc := range []struct {
			from []string
			to   string
			ok   bool
		}{
			{nil, "paused", false}, {nil, "running", true}, {nil, "cancelled", true},
			{[]string{"running"}, "paused", true}, {[]string{"running"}, "succeeded", true},
			{[]string{"running"}, "completed_with_failures", true}, {[]string{"running", "paused"}, "succeeded", false},
			{[]string{"running", "paused"}, "running", true}, {[]string{"running", "paused"}, "cancelled", true},
			{[]string{"running", "succeeded"}, "cancelled", false}, {[]string{"cancelled"}, "running", false},
		} {
			s := fresh()
			j := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Selector: "x"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
			if err := s.Create("a", j); err != nil {
				t.Fatal(err)
			}
			for _, st := range tc.from {
				if !s.UpdateStatus("a", "job", st) {
					t.Fatalf("setup %s", st)
				}
			}
			before, _ := s.Get("a", "job")
			if got := s.UpdateStatus("a", "job", tc.to); got != tc.ok {
				t.Fatalf("%v -> %s = %v", tc.from, tc.to, got)
			}
			after, _ := s.Get("a", "job")
			if !tc.ok && after.Status != before.Status {
				t.Fatal("rejected transition changed status")
			}
			if tc.ok && tc.to == "running" && after.StartedAt == nil {
				t.Fatal("missing start time")
			}
			if tc.ok && (tc.to == "cancelled" || tc.to == "succeeded" || tc.to == "completed_with_failures") && after.CompletedAt == nil {
				t.Fatal("missing completion time")
			}
		}
	})
	t.Run("IsolationTasksSnapshots", func(t *testing.T) {
		s := fresh()
		j := apiv1.Job{ID: "z", Target: apiv1.JobTarget{Devices: []string{"d"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}
		if err := s.Create("a", j); err != nil {
			t.Fatal(err)
		}
		if err := s.Create("a", j); err == nil {
			t.Fatal("duplicate accepted")
		}
		j.Target.Devices[0] = "changed"
		j.Action.Operation[0] = 'x'
		if _, ok := s.Get("b", "z"); ok {
			t.Fatal("tenant leak")
		}
		other := apiv1.Job{ID: "z", Name: "other", Target: apiv1.JobTarget{Selector: "all"}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
		if err := s.Create("b", other); err != nil {
			t.Fatal(err)
		}
		other.ID = "a"
		if err := s.Create("a", other); err != nil {
			t.Fatal(err)
		}
		if list := s.List("a"); len(list) != 2 || list[0].ID != "a" || list[1].ID != "z" {
			t.Fatalf("list: %+v", list)
		}
		got, ok := s.Get("a", "z")
		if !ok || got.Target.Devices[0] != "d" || got.Action.Operation[0] != '{' || got.Status != "scheduled" {
			t.Fatalf("snapshot: %+v", got)
		}
		got.Target.Devices[0] = "changed"
		got.Action.Operation[0] = 'x'
		got, _ = s.Get("a", "z")
		if got.Target.Devices[0] != "d" || got.Action.Operation[0] != '{' {
			t.Fatal("read alias")
		}
		for i, st := range []string{"pending", "running", "succeeded", "failed", "expired", "cancelled"} {
			if !s.PutTask("a", "z", apiv1.JobTask{Device: fmt.Sprint(i), Status: st}) {
				t.Fatal(st)
			}
		}
		if s.PutTask("b", "missing", apiv1.JobTask{Device: "x", Status: "failed"}) || s.PutTask("a", "z", apiv1.JobTask{Device: "x", Status: "bad"}) {
			t.Fatal("invalid task")
		}
		got, _ = s.Get("a", "z")
		if got.Counts != (apiv1.JobCounts{Total: 6, Pending: 1, Running: 1, Succeeded: 1, Failed: 1, Expired: 1, Cancelled: 1}) {
			t.Fatal(got.Counts)
		}
		if tasks := s.ListTasks("a", "z", "failed"); len(tasks) != 1 || tasks[0].Device != "3" {
			t.Fatal(tasks)
		}
		if got, ok := s.Get("b", "z"); !ok || got.Name != "other" || len(s.ListTasks("b", "z", "")) != 0 {
			t.Fatal("tenant list leak")
		}
		if !s.UpdateStatus("a", "z", "running") || s.UpdateStatus("a", "z", "scheduled") || s.UpdateStatus("b", "missing", "paused") {
			t.Fatal("transition")
		}
	})
	t.Run("CancelPendingTasks", func(t *testing.T) {
		s := fresh()
		for _, tenant := range []string{"a", "b"} {
			j := apiv1.Job{ID: "job", Target: apiv1.JobTarget{Devices: []string{"d"}}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
			if err := s.Create(tenant, j); err != nil {
				t.Fatal(err)
			}
			for _, task := range []apiv1.JobTask{{Device: "pending", Status: "pending"}, {Device: "running", Status: "running", Operation: "op"}, {Device: "done", Status: "succeeded"}} {
				if !s.PutTask(tenant, "job", task) {
					t.Fatal(task)
				}
			}
		}
		if !s.UpdateStatus("a", "job", "cancelled") {
			t.Fatal("cancel failed")
		}
		job, _ := s.Get("a", "job")
		if job.Counts != (apiv1.JobCounts{Total: 3, Running: 1, Succeeded: 1, Cancelled: 1}) {
			t.Fatalf("cancel counts: %+v", job.Counts)
		}
		other, _ := s.Get("b", "job")
		if other.Counts.Pending != 1 || other.Status != "scheduled" {
			t.Fatalf("other tenant: %+v", other)
		}
	})
	t.Run("EmptyDevices", func(t *testing.T) {
		s := fresh()
		j := apiv1.Job{ID: "x", Target: apiv1.JobTarget{Devices: []string{}}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
		if err := s.Create("a", j); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get("a", "x")
		if got.Target.Devices == nil {
			t.Fatal("empty devices lost")
		}
	})
}
