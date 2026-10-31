package storetest

import (
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func TestMemoryJobReplays(t *testing.T) {
	JobReplays(t, func(t *testing.T) (apiv1.JobStore, apiv1.JobReplayStore, func(string, string, time.Duration)) {
		t.Helper()
		now := time.Now()
		jobs := apiv1.NewMemoryJobStoreWithReplayClock(func() time.Time { return now })
		replay, ok := jobs.(apiv1.JobReplayStore)
		if !ok {
			t.Fatal("memory store does not implement JobReplayStore")
		}
		return jobs, replay, func(_, _ string, age time.Duration) { now = now.Add(age) }
	})
}
