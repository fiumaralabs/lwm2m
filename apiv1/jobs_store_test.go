package apiv1

import (
	"encoding/json"
	"fmt"
	"testing"
)

func validJob() Job {
	return Job{ID: "job_1", Target: JobTarget{Devices: []string{"d"}}, Action: JobAction{Operation: json.RawMessage(`{"type":"read"}`)}, Rollout: JobRollout{MaxConcurrent: 1, FailureThresholdPercent: 50}, TTLSeconds: 60}
}
func TestJobValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Job)
		valid  bool
	}{
		{"valid", func(*Job) {}, true}, {"no action", func(j *Job) { j.Action.Operation = nil }, false}, {"two actions", func(j *Job) { j.Action.Observation = json.RawMessage(`{}`) }, false}, {"no target", func(j *Job) { j.Target.Devices = nil }, false}, {"two targets", func(j *Job) { j.Target.Selector = "a=b" }, false}, {"concurrency", func(j *Job) { j.Rollout.MaxConcurrent = 0 }, false}, {"threshold low", func(j *Job) { j.Rollout.FailureThresholdPercent = -1 }, false}, {"threshold high", func(j *Job) { j.Rollout.FailureThresholdPercent = 101 }, false}, {"threshold zero", func(j *Job) { j.Rollout.FailureThresholdPercent = 0 }, true}, {"threshold hundred", func(j *Job) { j.Rollout.FailureThresholdPercent = 100 }, true}, {"ttl", func(j *Job) { j.TTLSeconds = 0 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := validJob()
			tt.change(&j)
			if got := ValidateJob(j) == nil; got != tt.valid {
				t.Fatalf("valid = %v", got)
			}
		})
	}
}
func TestJobTransitions(t *testing.T) {
	tests := []struct {
		from, to string
		ok       bool
	}{{"scheduled", "running", true}, {"scheduled", "paused", false}, {"running", "paused", true}, {"paused", "running", true}, {"running", "succeeded", true}, {"running", "completed_with_failures", true}, {"paused", "succeeded", false}, {"scheduled", "cancelled", true}, {"running", "cancelled", true}, {"paused", "cancelled", true}, {"succeeded", "cancelled", false}, {"completed_with_failures", "running", false}, {"cancelled", "running", false}, {"running", "unknown", false}}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if got := validJobTransition(tt.from, tt.to); got != tt.ok {
				t.Fatalf("%s -> %s: %v", tt.from, tt.to, got)
			}
		})
	}
}
func TestJobStoreIsolationCountsAndClones(t *testing.T) {
	s := NewMemoryJobStore()
	j := validJob()
	if err := s.Create("a", j); err != nil {
		t.Fatal(err)
	}
	j.Target.Devices[0] = "changed"
	j.Action.Operation[0] = 'x'
	if _, ok := s.Get("b", j.ID); ok {
		t.Fatal("tenant leak")
	}
	got, ok := s.Get("a", j.ID)
	if !ok || got.Target.Devices[0] != "d" || got.Action.Operation[0] != '{' {
		t.Fatal("write alias", got)
	}
	got.Target.Devices[0] = "changed"
	got.Action.Operation[0] = 'x'
	got, _ = s.Get("a", j.ID)
	if got.Target.Devices[0] != "d" || got.Action.Operation[0] != '{' {
		t.Fatal("read alias")
	}
	for i, st := range []string{"pending", "running", "succeeded", "failed", "expired", "cancelled"} {
		if !s.PutTask("a", j.ID, JobTask{Device: fmt.Sprint(i), Status: st}) {
			t.Fatal(st)
		}
	}
	if s.PutTask("b", j.ID, JobTask{Device: "x", Status: "failed"}) {
		t.Fatal("foreign write")
	}
	got, _ = s.Get("a", j.ID)
	if got.Counts != (JobCounts{Total: 6, Pending: 1, Running: 1, Succeeded: 1, Failed: 1, Expired: 1, Cancelled: 1}) {
		t.Fatal(got.Counts)
	}
	if tasks := s.ListTasks("a", j.ID, "failed"); len(tasks) != 1 || tasks[0].Device != "3" {
		t.Fatal(tasks)
	}
	if len(s.List("b")) != 0 || len(s.ListTasks("b", j.ID, "")) != 0 {
		t.Fatal("tenant list leak")
	}
	if !s.UpdateStatus("a", j.ID, "running") || s.UpdateStatus("a", j.ID, "scheduled") || s.UpdateStatus("b", j.ID, "paused") {
		t.Fatal("status transition")
	}
}
func TestJobEmptyTargetSnapshot(t *testing.T) {
	s := NewMemoryJobStore()
	j := validJob()
	j.Target.Devices = []string{}
	if err := s.Create("a", j); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("a", j.ID)
	if got.Target.Devices == nil {
		t.Fatal("empty devices changed to nil")
	}
}

func TestJobPauseRule(t *testing.T) {
	for _, tt := range []struct {
		finished, failed, threshold int
		want                        bool
	}{{9, 9, 50, false}, {10, 4, 50, false}, {10, 5, 50, true}, {11, 5, 50, false}, {11, 6, 50, true}, {10, 0, 0, true}, {0, 0, 0, false}} {
		if got := ShouldPauseJob(tt.failed, tt.finished, tt.threshold); got != tt.want {
			t.Fatalf("%+v got %v", tt, got)
		}
	}
}
