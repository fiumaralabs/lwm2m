package apiv1

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMemoryJobReplayConcurrencyAndExpiry(t *testing.T) {
	s := NewMemoryJobStore().(*memoryJobStore)
	const n = 32
	results := make([]JobCreateResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j := Job{ID: fmt.Sprintf("job-%d", i), Target: JobTarget{Devices: []string{"device"}}, Action: JobAction{Operation: json.RawMessage(`{}`)}, Rollout: JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
			results[i], errs[i] = s.CreateResult("tenant", "key", JobCreateResult{Job: &j})
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || !reflect.DeepEqual(results[0], results[i]) {
			t.Fatalf("result %d: %#v, %v", i, results[i], errs[i])
		}
	}
	if got := len(s.List("tenant")); got != 1 {
		t.Fatalf("jobs = %d, want 1", got)
	}
	s.mu.Lock()
	record := s.replays["tenant"]["key"]
	record.expires = time.Now().Add(-time.Second)
	s.replays["tenant"]["key"] = record
	s.mu.Unlock()
	if _, ok := s.ReplayCreate("tenant", "key"); ok {
		t.Fatal("expired replay was returned")
	}
	problem := JobCreateResult{Problem: &Problem{Title: "new"}}
	if got, err := s.CreateResult("tenant", "key", problem); err != nil || !reflect.DeepEqual(got, problem) {
		t.Fatalf("after expiry: %#v, %v", got, err)
	}
	if _, err := s.CreateResult("tenant", "", problem); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ReplayCreate("tenant", ""); ok {
		t.Fatal("empty key was recorded")
	}
}

func TestMemoryJobReplaySeparateInterface(t *testing.T) {
	type replayStore interface {
		ReplayCreate(string, string) (JobCreateResult, bool)
		CreateResult(string, string, JobCreateResult) (JobCreateResult, error)
	}
	s, ok := NewMemoryJobStore().(replayStore)
	if !ok {
		t.Fatal("memory job store must implement the separate replay interface")
	}
	first := JobCreateResult{Problem: &Problem{Title: "first"}}
	got, err := s.CreateResult("a", "key", first)
	if err != nil || got.Problem == nil || got.Problem.Title != "first" {
		t.Fatalf("first: %#v, %v", got, err)
	}
	got, err = s.CreateResult("a", "key", JobCreateResult{Problem: &Problem{Title: "second"}})
	if err != nil || got.Problem == nil || got.Problem.Title != "first" {
		t.Fatalf("replay: %#v, %v", got, err)
	}
	if _, ok := s.ReplayCreate("b", "key"); ok {
		t.Fatal("replay leaked to another tenant")
	}
	if _, ok := s.ReplayCreate("a", "key"); !ok {
		t.Fatal("replay not found")
	}
	b, err := json.Marshal(first)
	if err != nil || string(b) == "{}" {
		t.Fatalf("marshal: %s, %v", b, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["problem"]; !ok {
		t.Fatalf("missing lower-case problem field: %s", b)
	}
	if _, ok := fields["job"]; ok {
		t.Fatalf("nil job must be omitted: %s", b)
	}
}
