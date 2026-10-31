package apiv1

import (
	"context"
	"testing"
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
