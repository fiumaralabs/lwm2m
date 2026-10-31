package apiv1

import (
	"context"
	"testing"
	"time"
)

func TestJobRunnerCompletesTerminalTasks(t *testing.T) {
	for _, tc := range []struct{ name, taskStatus, want string }{
		{"success", "succeeded", "succeeded"},
		{"failure", "failed", "completed_with_failures"},
		{"expired", "expired", "completed_with_failures"},
		{"cancelled", "cancelled", "completed_with_failures"},
		{"pending", "pending", "running"},
		{"running", "running", "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := NewMemoryJobStore()
			j := Job{ID: "job", Target: JobTarget{Devices: []string{"dev"}}, Action: JobAction{Operation: []byte(`{}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
			if err := jobs.Create("tenant", j); err != nil {
				t.Fatal(err)
			}
			if !jobs.UpdateStatus("tenant", "job", "running") || !jobs.PutTask("tenant", "job", JobTask{Device: "dev", Status: tc.taskStatus}) {
				t.Fatal("setup failed")
			}
			if err := (JobRunner{Jobs: jobs}).Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := jobs.Get("tenant", "job")
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s", got.Status, tc.want)
			}
			if (got.CompletedAt != nil) != (tc.want != "running") {
				t.Fatalf("completedAt = %v", got.CompletedAt)
			}
		})
	}
}

func TestJobRunnerMixedTerminalTasksCompleteWithFailures(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "mixed", Target: JobTarget{Devices: []string{"a", "b"}}, Action: JobAction{Operation: []byte(`{}`)}, Rollout: JobRollout{MaxConcurrent: 2}, TTLSeconds: 1}
	if err := jobs.Create("t", job); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.StartJob("t", "mixed", []string{"a", "b"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, task := range []JobTask{{Device: "a", Status: "succeeded"}, {Device: "b", Status: "failed"}} {
		if !jobs.PutTask("t", "mixed", task) {
			t.Fatal(task)
		}
	}
	if err := (JobRunner{Jobs: jobs}).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := jobs.Get("t", "mixed")
	if got.Status != "completed_with_failures" || got.Counts.Succeeded != 1 || got.Counts.Failed != 1 {
		t.Fatalf("job=%+v", got)
	}
}
