package operations

import (
	"errors"
	"reflect"
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

func TestQueuedEncodingFailure(t *testing.T) {
	now := time.Now().UTC()
	completed := now.Add(time.Second)
	op := New("t", "d", "write", nil, nil, now)
	failure := &Error{Code: "encode", Message: "cannot encode request"}
	if err := op.Apply(Change{Status: Failed, At: completed, Error: failure}); err != nil {
		t.Fatal(err)
	}
	if op.Status != Failed || op.CompletedAt == nil || !op.CompletedAt.Equal(completed) || op.SentAt != nil || !reflect.DeepEqual(op.Error, failure) {
		t.Fatalf("pre-send failure: %+v", op)
	}
	before := op
	for _, status := range []Status{Queued, Sent, Succeeded, Failed, Timeout, Expired, Dropped, Cancelled} {
		if err := op.Apply(Change{Status: status, At: completed.Add(time.Second)}); !errors.Is(err, ErrTransition) {
			t.Fatalf("failed -> %s: %v", status, err)
		}
		if !reflect.DeepEqual(op, before) {
			t.Fatalf("rejected transition to %s mutated operation", status)
		}
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
