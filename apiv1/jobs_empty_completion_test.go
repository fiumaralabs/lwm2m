package apiv1

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerCompletesEmptySnapshot(t *testing.T) {
	jobs := NewMemoryJobStore()
	job := Job{ID: "empty", Target: JobTarget{Selector: "env=prod"}, Action: JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/0"]}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 1}
	if err := jobs.Create("a", job); err != nil {
		t.Fatal(err)
	}
	r := JobRunner{Jobs: jobs, Devices: &memory.Devices{}}
	for i := 0; i < 2; i++ {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := jobs.Get("a", job.ID)
	if !ok || got.Status != "succeeded" || got.CompletedAt == nil || got.CompletedAt.After(time.Now().UTC()) || got.Counts.Total != 0 {
		t.Fatalf("empty job was not completed: %+v", got)
	}
}
