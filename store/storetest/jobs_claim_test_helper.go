package storetest

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// JobClaims checks that simultaneous claimers cannot exceed the job's rollout limit.
func JobClaims(t *testing.T, s apiv1.JobStore) {
	t.Helper()
	claim, ok := s.(interface {
		ClaimTasks(string, string) ([]apiv1.JobTask, error)
	})
	if !ok {
		t.Fatal("store does not support atomic task claims")
	}
	job := apiv1.Job{ID: "claim", Target: apiv1.JobTarget{Devices: []string{"a"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 3}, TTLSeconds: 60}
	if err := s.Create("a", job); err != nil {
		t.Fatal(err)
	}
	var devices []string
	for i := 0; i < 10; i++ {
		devices = append(devices, fmt.Sprint(i))
	}
	if ok, err := s.StartJob("a", job.ID, devices, time.Now()); !ok || err != nil {
		t.Fatalf("start: %v %v", ok, err)
	}
	var wg sync.WaitGroup
	results := make(chan []apiv1.JobTask, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tasks, err := claim.ClaimTasks("a", job.ID); results <- tasks; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for tasks := range results {
		for _, task := range tasks {
			if seen[task.Device] || task.Status != "dispatching" {
				t.Fatalf("duplicate or invalid claim: %+v", task)
			}
			seen[task.Device] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("claimed %d tasks, want 3", len(seen))
	}
	j, _ := s.Get("a", job.ID)
	if j.Counts.Running != 3 || j.Counts.Pending != 7 {
		t.Fatalf("counts: %+v", j.Counts)
	}
	for device := range seen {
		if !s.PutTask("a", job.ID, apiv1.JobTask{Device: device, Status: "succeeded"}) {
			t.Fatal("complete task")
		}
		break
	}
	if !s.UpdateStatus("a", job.ID, "paused") {
		t.Fatal("pause")
	}
	tasks, err := claim.ClaimTasks("a", job.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("paused claim: %v %v", tasks, err)
	}
	tasks, err = claim.ClaimTasks("b", job.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("foreign claim: %v %v", tasks, err)
	}
	if !s.UpdateStatus("a", job.ID, "running") {
		t.Fatal("resume")
	}
	tasks, err = claim.ClaimTasks("a", job.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("resume claim: %v %v", tasks, err)
	}
	if !s.UpdateStatus("a", job.ID, "cancelled") {
		t.Fatal("cancel")
	}
	// The runner can release a claim after cancellation, e.g. on admission failure.
	dispatching := s.ListTasks("a", job.ID, "dispatching")
	if len(dispatching) != 3 {
		t.Fatalf("dispatching: %+v", dispatching)
	}
	for _, task := range dispatching {
		task.Status = "pending"
		if !s.PutTask("a", job.ID, task) {
			t.Fatal("release claim")
		}
	}
	j, _ = s.Get("a", job.ID)
	if j.Status != "cancelled" || j.Counts.Pending != 0 || j.Counts.Cancelled != 9 || j.Counts.Running != 0 {
		t.Fatalf("cancelled release counts: status=%s counts=%+v", j.Status, j.Counts)
	}
	tasks, err = claim.ClaimTasks("a", job.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("cancelled claim: %v %v", tasks, err)
	}
}
