package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
)

func TestAtomicClaimAndResultSnapshots(t *testing.T) {
	s, ctx, now := NewOperations(), context.Background(), time.Now().UTC()
	o := operations.New("t", "d", "read", nil, nil, now)
	if err := s.Create(ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if s.Update(ctx, "t", o.ID, operations.Queued, operations.Change{Status: operations.Sent, At: now}) == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("delivery claimed more than once")
	}
	result, failure := []byte(`{"code":"4.04"}`), &operations.Error{Code: "not_found"}
	if err := s.Update(ctx, "t", o.ID, operations.Sent, operations.Change{Status: operations.Failed, At: now, Result: result, Error: failure}); err != nil {
		t.Fatal(err)
	}
	result[0] = '!'
	failure.Code = "changed"
	got, _ := s.Get(ctx, "t", o.ID)
	if got.Status != operations.Failed || string(got.Result) != `{"code":"4.04"}` || got.Error.Code != "not_found" {
		t.Fatalf("result: %+v", got)
	}
	got.Result[0] = '!'
	got.Error.Code = "changed"
	got, _ = s.Get(ctx, "t", o.ID)
	if got.Result[0] != '{' || got.Error.Code != "not_found" {
		t.Fatal("result aliases store")
	}
	for _, read := range []func(context.Context, string, string) ([]operations.Operation, error){s.List, s.Pending} {
		list, err := read(ctx, "other", "d")
		if err != nil || len(list) != 0 {
			t.Fatal("cross-tenant list")
		}
	}
}
