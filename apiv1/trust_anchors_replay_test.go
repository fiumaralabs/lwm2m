package apiv1

import (
	"testing"
	"time"
)

func TestTrustAnchorReplayExpiryAndFailures(t *testing.T) {
	s := NewMemoryTrustAnchorStore(2).(*memoryTrustAnchorStore)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	failed := TrustAnchorResult{Problem: &Problem{Status: 400, Code: "invalid_trust_anchor", Detail: "invalid"}}
	if got := s.CreateResult("a", "key", failed); got.Problem == nil || got.Problem.Status != 400 {
		t.Fatal(got)
	}
	if got, ok := s.Replay("a", "key"); !ok || got.Problem.Code != "invalid_trust_anchor" {
		t.Fatal(got, ok)
	}
	if _, ok := s.Replay("b", "key"); ok {
		t.Fatal("tenant leak")
	}
	now = now.Add(24*time.Hour - time.Nanosecond)
	if _, ok := s.Replay("a", "key"); !ok {
		t.Fatal("expired early")
	}
	now = now.Add(time.Nanosecond)
	if _, ok := s.Replay("a", "key"); ok {
		t.Fatal("not expired at boundary")
	}
	a := TrustAnchor{ID: "new"}
	if got := s.CreateResult("a", "key", TrustAnchorResult{Anchor: &a}); got.Anchor == nil || got.Anchor.ID != "new" {
		t.Fatal(got)
	}
}
