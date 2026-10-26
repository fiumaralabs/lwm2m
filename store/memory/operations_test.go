package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

func TestOperations(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := NewOperations()
	var _ store.Operations = s
	a := operations.New("a", "device", "read", []byte(`{"paths":["/3/0"]}`), nil, now)
	b := operations.New("a", "device", "read", nil, nil, now.Add(time.Second))
	for _, op := range []operations.Operation{a, b} {
		if err := s.Create(ctx, "a", op); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, "b", a); !errors.Is(err, store.ErrTenant) {
		t.Fatalf("tenant mismatch: %v", err)
	}
	if err := s.Create(ctx, "a", a); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.Get(ctx, "b", a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
	list, _ := s.List(ctx, "a", "device")
	pending, _ := s.Pending(ctx, "a", "device")
	if len(list) != 2 || list[0].ID != b.ID || len(pending) != 2 || pending[0].ID != a.ID {
		t.Fatal("ordering")
	}
	a.Request[0] = '!'
	list[1].Request[0] = '!'
	got, _ := s.Get(ctx, "a", a.ID)
	if got.Request[0] != '{' {
		t.Fatal("store aliases request")
	}
	if err := s.Update(ctx, "b", a.ID, operations.Queued, operations.Change{Status: operations.Cancelled, At: now}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Update(ctx, "a", a.ID, operations.Queued, operations.Change{Status: operations.Cancelled, At: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, "a", a.ID, operations.Queued, operations.Change{Status: operations.Sent, At: now}); !errors.Is(err, operations.ErrTransition) {
		t.Fatalf("CAS: %v", err)
	}
	pending, _ = s.Pending(ctx, "a", "device")
	if len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatal("cancelled still pending")
	}
	got, _ = s.Get(ctx, "a", a.ID)
	*got.CompletedAt = time.Time{}
	got, _ = s.Get(ctx, "a", a.ID)
	if !got.CompletedAt.Equal(now) {
		t.Fatal("store aliases timestamp")
	}
	cancel, cancelFunc := context.WithCancel(ctx)
	cancelFunc()
	if _, err := s.Get(cancel, "a", a.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
