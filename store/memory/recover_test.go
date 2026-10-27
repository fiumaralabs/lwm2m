package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestRecoverUnfinished(t *testing.T) {
	s := memory.NewOperations()
	ctx := context.Background()
	now := time.Now()
	for i, tenant := range []string{"b", "a", "c"} {
		o := operations.New(tenant, "dev", "read", []byte(`{"paths":["/3/0/0"]}`), nil, now)
		o.ID = tenant
		if err := s.Create(ctx, tenant, o); err != nil {
			t.Fatal(err)
		}
		if i != 0 {
			if err := s.Update(ctx, tenant, o.ID, operations.Queued, operations.Change{Status: operations.Sent, At: now}); err != nil {
				t.Fatal(err)
			}
		}
		if i == 2 {
			if err := s.Update(ctx, tenant, o.ID, operations.Sent, operations.Change{Status: operations.Succeeded, At: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, ok := any(s).(operations.Recoverer)
	if !ok {
		t.Fatal("memory store does not support recovery")
	}
	got, err := r.Unfinished(ctx)
	if err != nil || len(got) != 2 || got[0].Tenant != "a" || got[1].Tenant != "b" {
		t.Fatalf("unfinished=%+v err=%v", got, err)
	}
	got[0].Request[0] = '!'
	again, _ := r.Unfinished(ctx)
	if again[0].Request[0] != '{' {
		t.Fatal("snapshot aliases store")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Unfinished(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
