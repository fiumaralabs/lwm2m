package operations

import (
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	now := time.Now().UTC()
	op := New("tenant", "device", "read", nil, nil, now)
	if op.Status != Queued || op.TTLSeconds != 86400 || op.WhenOffline != "queue" || !op.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("defaults: %+v", op)
	}
	if err := op.Apply(Change{Status: Sent, At: now}); err != nil || op.SentAt == nil {
		t.Fatalf("send: %+v, %v", op, err)
	}
	if err := op.Apply(Change{Status: Succeeded, At: now}); err != nil || op.CompletedAt == nil {
		t.Fatalf("finish: %+v, %v", op, err)
	}
	if err := op.Apply(Change{Status: Cancelled, At: now}); err == nil {
		t.Fatal("terminal operation changed")
	}
}

func TestQueuedTransitions(t *testing.T) {
	now := time.Now().UTC()
	zero := 0
	for _, status := range []Status{Cancelled, Expired, Dropped} {
		op := New("t", "d", "execute", nil, &zero, now)
		if !op.ExpiresAt.Equal(now) {
			t.Fatal("zero TTL must mean now or never")
		}
		if err := op.Apply(Change{Status: status, At: now}); err != nil || op.CompletedAt == nil {
			t.Fatalf("%s: %v", status, err)
		}
	}
	op := New("t", "d", "read", nil, nil, now)
	if err := op.Apply(Change{Status: Succeeded, At: now}); err == nil {
		t.Fatal("queued cannot succeed before sending")
	}
}
