package storetest

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// JobReplays exercises the same replay contract for each backend. ageReplay
// subtracts age from a stored record's expiration without waiting in real time.
func JobReplays(t *testing.T, fresh func(*testing.T) (apiv1.JobStore, apiv1.JobReplayStore, func(tenant, key string, age time.Duration))) {
	t.Helper()
	job := func(id string) apiv1.JobCreateResult {
		return apiv1.JobCreateResult{Job: &apiv1.Job{ID: id, Name: id, Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: []byte(`{}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 60}}
	}
	create := func(t *testing.T, s apiv1.JobReplayStore, tenant, key string, result apiv1.JobCreateResult) apiv1.JobCreateResult {
		t.Helper()
		got, err := s.CreateResult(tenant, key, result)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	check := func(t *testing.T, got, want apiv1.JobCreateResult) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("result = %#v, want %#v", got, want)
		}
	}
	count := func(t *testing.T, s apiv1.JobStore, tenant string, want int) {
		t.Helper()
		if n := len(s.List(tenant)); n != want {
			t.Fatalf("%s jobs = %d, want %d", tenant, n, want)
		}
	}
	t.Run("JobReplayAndTenantIsolation", func(t *testing.T) {
		jobs, replay, _ := fresh(t)
		first := create(t, replay, "a", "key", job("first"))
		got, ok := replay.ReplayCreate("a", "key")
		if !ok {
			t.Fatal("missing replay")
		}
		check(t, got, first)
		check(t, create(t, replay, "a", "key", job("loser")), first)
		count(t, jobs, "a", 1)
		if _, ok := replay.ReplayCreate("b", "key"); ok {
			t.Fatal("tenant replay leaked")
		}
		other := create(t, replay, "b", "key", job("other"))
		if other.Job == nil || other.Job.ID != "other" {
			t.Fatalf("tenant b: %#v", other)
		}
		check(t, create(t, replay, "b", "key", job("loser-b")), other)
		count(t, jobs, "a", 1)
		count(t, jobs, "b", 1)
	})
	t.Run("ProblemReplay", func(t *testing.T) {
		jobs, replay, _ := fresh(t)
		problem := apiv1.JobCreateResult{Problem: &apiv1.Problem{Title: "first", Detail: "original"}}
		first := create(t, replay, "a", "key", problem)
		check(t, first, problem)
		got, ok := replay.ReplayCreate("a", "key")
		if !ok {
			t.Fatal("missing problem replay")
		}
		check(t, got, first)
		check(t, create(t, replay, "a", "key", job("not-created")), first)
		count(t, jobs, "a", 0)
	})
	t.Run("EmptyKey", func(t *testing.T) {
		jobs, replay, _ := fresh(t)
		for _, id := range []string{"first", "second"} {
			got := create(t, replay, "a", "", job(id))
			if got.Job == nil || got.Job.ID != id {
				t.Fatalf("plain create: %#v", got)
			}
		}
		if _, ok := replay.ReplayCreate("a", ""); ok {
			t.Fatal("empty key recorded")
		}
		count(t, jobs, "a", 2)
	})
	t.Run("Expiry24Hours", func(t *testing.T) {
		jobs, replay, age := fresh(t)
		if age == nil {
			t.Fatal("backend must provide deterministic replay aging")
		}
		first := create(t, replay, "a", "key", job("first"))
		age("a", "key", 23*time.Hour)
		got, ok := replay.ReplayCreate("a", "key")
		if !ok {
			t.Fatal("replay expired before 24 hours")
		}
		check(t, got, first)
		check(t, create(t, replay, "a", "key", job("too-early")), first)
		count(t, jobs, "a", 1)
		age("a", "key", 2*time.Hour)
		if _, ok := replay.ReplayCreate("a", "key"); ok {
			t.Fatal("replay survives past 24 hours")
		}
		next := create(t, replay, "a", "key", job("after-expiry"))
		if next.Job == nil || next.Job.ID != "after-expiry" {
			t.Fatalf("after expiry: %#v", next)
		}
		count(t, jobs, "a", 2)
	})
	t.Run("ConcurrentFirstWins", func(t *testing.T) {
		jobs, replay, _ := fresh(t)
		const n = 32
		results := make([]apiv1.JobCreateResult, n)
		errs := make([]error, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = replay.CreateResult("a", "key", job(fmt.Sprintf("job-%d", i)))
			}(i)
		}
		close(start)
		wg.Wait()
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("caller %d: %v", i, errs[i])
			}
			check(t, results[i], results[0])
		}
		if results[0].Job == nil {
			t.Fatal("winning job missing")
		}
		count(t, jobs, "a", 1)
		got, ok := replay.ReplayCreate("a", "key")
		if !ok {
			t.Fatal("missing winning replay")
		}
		check(t, got, results[0])
	})
}
