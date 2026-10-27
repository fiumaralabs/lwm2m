package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

func operationsQueuedEncodingFailure(t *testing.T, factory func(*testing.T) store.Operations) {
	ctx, now := context.Background(), time.Now().UTC()
	completed := now.Add(time.Second)
	s := factory(t)
	op := operations.New("tenant", "device", "write", nil, nil, now)
	if err := s.Create(ctx, "tenant", op); err != nil {
		t.Fatal(err)
	}
	failure := &operations.Error{Code: "encode", Message: "cannot encode request"}
	if err := s.Update(ctx, "tenant", op.ID, operations.Queued, operations.Change{Status: operations.Failed, At: completed, Error: failure}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "tenant", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != operations.Failed || got.CompletedAt == nil || !got.CompletedAt.Equal(completed) || got.SentAt != nil || got.Error == nil || *got.Error != *failure {
		t.Fatalf("stored pre-send failure: %+v", got)
	}
	pending, err := s.Pending(ctx, "tenant", "device")
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed operation still pending: %+v, %v", pending, err)
	}
	for _, status := range []operations.Status{operations.Queued, operations.Sent, operations.Succeeded, operations.Failed, operations.Timeout, operations.Expired, operations.Dropped, operations.Cancelled} {
		if err := s.Update(ctx, "tenant", op.ID, operations.Failed, operations.Change{Status: status, At: completed.Add(time.Second)}); !errors.Is(err, operations.ErrTransition) {
			t.Fatalf("failed -> %s: %v", status, err)
		}
	}
	got, err = s.Get(ctx, "tenant", op.ID)
	if err != nil || got.Status != operations.Failed || got.CompletedAt == nil || !got.CompletedAt.Equal(completed) || got.SentAt != nil || got.Error == nil || *got.Error != *failure {
		t.Fatalf("rejected transitions changed stored failure: %+v, %v", got, err)
	}
}

func operationsList(t *testing.T, factory func(*testing.T) store.Operations) {
	ctx, now, s := context.Background(), time.Now().UTC(), factory(t)
	job := "job"
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		o := operations.New("t", "dev", "read", []byte(`{}`), nil, now)
		o.ID, o.Job = id, &job
		if i == 1 {
			o.Status = operations.Failed
		}
		if i == 2 {
			o.Type = "write"
		}
		if i == 3 {
			o.Device = "other"
		}
		if i == 4 {
			o.Job = nil
		}
		if err := s.Create(ctx, "t", o); err != nil {
			t.Fatal(err)
		}
	}
	f := operations.ListFilter{Device: "dev", Status: []operations.Status{operations.Queued, operations.Failed}, Type: "read", Job: job}
	items, next, err := s.List(ctx, "t", f, "", 1)
	if err != nil || len(items) != 1 || items[0].ID != "b" || next == "" {
		t.Fatalf("first page: %+v %q %v", items, next, err)
	}
	items[0].Request[0], *items[0].Job = '!', "changed"
	got, _ := s.Get(ctx, "t", "b")
	if string(got.Request) != `{}` || *got.Job != job {
		t.Fatal("list aliases record")
	}
	items, end, err := s.List(ctx, "t", f, next, 1)
	if err != nil || len(items) != 1 || items[0].ID != "a" || end != "" {
		t.Fatalf("last page: %+v %q %v", items, end, err)
	}
	for _, tenant := range []string{"", "other"} {
		items, _, err := s.List(ctx, tenant, operations.ListFilter{}, "", 50)
		if err != nil || len(items) != 0 {
			t.Fatalf("tenant %q: %+v %v", tenant, items, err)
		}
	}
	items, _, err = s.List(ctx, "t", operations.ListFilter{}, "", 50)
	if err != nil || len(items) != 5 || items[0].ID != "e" {
		t.Fatalf("unrestricted: %+v %v", items, err)
	}
	for _, tc := range []struct {
		tenant string
		f      operations.ListFilter
		cursor string
		limit  int
	}{
		{"t", f, "!", 1}, {"t", f, "e30", 1}, {"other", f, next, 1},
		{"t", operations.ListFilter{}, next, 1}, {"t", f, "", 0}, {"t", f, "", 501},
	} {
		if _, _, err := s.List(ctx, tc.tenant, tc.f, tc.cursor, tc.limit); !errors.Is(err, store.ErrCursor) {
			t.Fatalf("invalid query %+v: %v", tc, err)
		}
	}
	cancel, stop := context.WithCancel(ctx)
	stop()
	if _, _, err := s.List(cancel, "t", f, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func operationsBasic(t *testing.T, factory func(*testing.T) store.Operations) {
	ctx, now := context.Background(), time.Now().UTC()
	s := factory(t)
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
	list, _, _ := s.List(ctx, "a", operations.ListFilter{Device: "device"}, "", 50)
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

// Operations runs the behavioral contract against an empty store per subtest.
func Operations(t *testing.T, factory func(*testing.T) store.Operations) {
	t.Run("unfinished", func(t *testing.T) { operationsUnfinished(t, factory) })
	t.Run("concurrent", func(t *testing.T) { operationsConcurrent(t, factory) })
	t.Run("pagination_fifo", func(t *testing.T) {
		s, ctx := factory(t), context.Background()
		now := time.Unix(100, 0).UTC()
		for _, id := range []string{"e", "c", "a", "d", "b"} {
			o := operations.New("t", "dev", "read", nil, nil, now)
			o.ID = id
			if id == "e" {
				o.CreatedAt = now.Add(time.Second)
			}
			if err := s.Create(ctx, "t", o); err != nil {
				t.Fatal(err)
			}
		}
		pending, err := s.Pending(ctx, "t", "dev")
		if err != nil {
			t.Fatal(err)
		}
		var ids string
		for _, o := range pending {
			ids += o.ID
		}
		if ids != "abcde" {
			t.Fatalf("FIFO = %q", ids)
		}
		ids, cursor := "", ""
		for range 6 {
			page, next, err := s.List(ctx, "t", operations.ListFilter{}, cursor, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range page {
				ids += o.ID
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if ids != "edcba" {
			t.Fatalf("pagination = %q", ids)
		}
	})
	t.Run("basic", func(t *testing.T) { operationsBasic(t, factory) })
	t.Run("list", func(t *testing.T) { operationsList(t, factory) })
	t.Run("failure", func(t *testing.T) { operationsQueuedEncodingFailure(t, factory) })
}

func operationsUnfinished(t *testing.T, factory func(*testing.T) store.Operations) {
	s := factory(t)
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

func operationsConcurrent(t *testing.T, factory func(*testing.T) store.Operations) {
	s, ctx, now := factory(t), context.Background(), time.Now().UTC()
	o := operations.New("t", "dev", "read", []byte(`{}`), nil, now)
	if err := s.Create(ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, "t", o.ID, operations.Queued, operations.Change{Status: operations.Succeeded, At: now}); !errors.Is(err, operations.ErrTransition) {
		t.Fatalf("illegal edge: %v", err)
	}
	var wg sync.WaitGroup
	start, results := make(chan struct{}), make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			<-start
			results <- s.Update(ctx, "t", o.ID, operations.Queued, operations.Change{Status: operations.Sent, At: now})
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, operations.ErrTransition) {
			t.Fatalf("loser error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
	// Same identity is independent across tenants, including the empty tenant.
	for _, tenant := range []string{"other", ""} {
		o.Tenant = tenant
		if err := s.Create(ctx, tenant, o); err != nil {
			t.Fatal(err)
		}
		pending, err := s.Pending(ctx, tenant, "dev")
		if err != nil || len(pending) != 1 || pending[0].Tenant != tenant {
			t.Fatalf("pending isolation: %+v %v", pending, err)
		}
	}
	pending, err := s.Pending(ctx, "t", "dev")
	if err != nil || len(pending) != 0 {
		t.Fatalf("sent pending: %+v %v", pending, err)
	}
}
