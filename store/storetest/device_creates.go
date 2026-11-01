package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

// DeviceCreates tests the atomic device-create outcome contract on a fresh store.
func DeviceCreates(t *testing.T, factory func(*testing.T) (store.DeviceCreateStore, store.Devices)) {
	t.Helper()
	ctx := context.Background()
	t.Run("non-mutating lookup", func(t *testing.T) {
		s, _ := factory(t)
		if _, err := s.GetDeviceCreate(ctx, "a", "id"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		fence, err := s.GetOrFenceDeviceCreate(ctx, "a", "id")
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.GetDeviceCreate(ctx, "a", "id")
		if err != nil || !reflect.DeepEqual(fence, got) {
			t.Fatalf("lookup: %+v %v", got, err)
		}
		if _, err := s.GetDeviceCreate(ctx, "b", "id"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("tenant isolation: %v", err)
		}
	})
	t.Run("replay and tenant isolation", func(t *testing.T) {
		s, devices := factory(t)
		d := store.Device{Endpoint: "one"}
		first, created, err := s.CreateDeviceWithOutcome(ctx, "a", "id", "hash", d)
		if err != nil || !created || first.Status != "committed" || first.Endpoint != "one" || first.Tenant != "a" || first.OperationID != "id" || first.Fingerprint != "hash" || first.CreatedAt.IsZero() || first.CompletedAt.IsZero() {
			t.Fatalf("first: %+v %v %v", first, created, err)
		}
		again, created, err := s.CreateDeviceWithOutcome(ctx, "a", "id", "hash", store.Device{Endpoint: "other"})
		if err != nil || created || !reflect.DeepEqual(first, again) {
			t.Fatalf("replay: %+v %v %v", again, created, err)
		}
		_, created, err = s.CreateDeviceWithOutcome(ctx, "a", "id", "different", store.Device{Endpoint: "other"})
		if !errors.Is(err, store.ErrOperationIDReused) || created {
			t.Fatalf("reuse: %v %v", created, err)
		}
		if _, err := devices.Get("a", "other"); !errors.Is(err, store.ErrDeviceNotFound) {
			t.Fatalf("unexpected device: %v", err)
		}
		foreign, created, err := s.CreateDeviceWithOutcome(ctx, "b", "id", "different", store.Device{Endpoint: "two"})
		if err != nil || !created || foreign.Endpoint != "two" || foreign.Tenant != "b" {
			t.Fatalf("foreign: %+v %v %v", foreign, created, err)
		}
		conflict, created, err := s.CreateDeviceWithOutcome(ctx, "b", "conflict", "x", d)
		if err != nil || !created || conflict.Status != "not-created" || conflict.Reason != "endpoint-conflict" || conflict.Endpoint != "" {
			t.Fatalf("conflict: %+v %v %v", conflict, created, err)
		}
		if _, err := devices.Get("a", "one"); err != nil {
			t.Fatalf("owner lost device: %v", err)
		}
		if _, err := devices.Get("b", "one"); !errors.Is(err, store.ErrDeviceNotFound) {
			t.Fatalf("foreign copy: %v", err)
		}
		if err := s.DeleteDeviceCreate(ctx, "b", "id"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteDeviceCreate(ctx, "b", "id"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing delete: %v", err)
		}
		replay, err := s.GetOrFenceDeviceCreate(ctx, "a", "id")
		if err != nil || !reflect.DeepEqual(first, replay) {
			t.Fatalf("foreign delete affected owner: %+v %v", replay, err)
		}
	})
	t.Run("fence and invalid", func(t *testing.T) {
		s, devices := factory(t)
		fence, err := s.GetOrFenceDeviceCreate(ctx, "a", "id")
		if err != nil || fence.Status != "not-created" || fence.Reason != "fenced" || fence.Endpoint != "" || fence.CreatedAt.IsZero() || fence.CompletedAt.IsZero() {
			t.Fatalf("fence: %+v %v", fence, err)
		}
		got, created, err := s.CreateDeviceWithOutcome(ctx, "a", "id", "anything", store.Device{Endpoint: "one"})
		if err != nil || created || !reflect.DeepEqual(fence, got) {
			t.Fatalf("fenced create: %+v %v %v", got, created, err)
		}
		got, created, err = s.RecordDeviceCreateNotCreated(ctx, "a", "id", "other", "invalid")
		if err != nil || created || !reflect.DeepEqual(fence, got) {
			t.Fatalf("fenced record: %+v %v", got, err)
		}
		if _, err := devices.Get("a", "one"); !errors.Is(err, store.ErrDeviceNotFound) {
			t.Fatalf("fenced device: %v", err)
		}
		invalid, inserted, err := s.RecordDeviceCreateNotCreated(ctx, "a", "invalid", "hash", "invalid")
		if err != nil || !inserted || invalid.Status != "not-created" || invalid.Reason != "invalid" || invalid.Endpoint != "" {
			t.Fatalf("invalid: %+v %v", invalid, err)
		}
		replay, inserted, err := s.RecordDeviceCreateNotCreated(ctx, "a", "invalid", "hash", "invalid")
		if err != nil || inserted || !reflect.DeepEqual(invalid, replay) {
			t.Fatalf("invalid replay: %+v %v", replay, err)
		}
		_, inserted, err = s.RecordDeviceCreateNotCreated(ctx, "a", "invalid", "other", "invalid")
		if inserted || !errors.Is(err, store.ErrOperationIDReused) {
			t.Fatalf("invalid reuse: %v", err)
		}
		other, err := s.GetOrFenceDeviceCreate(ctx, "b", "invalid")
		if err != nil || other.Reason != "fenced" || other.Tenant != "b" {
			t.Fatalf("foreign fence: %+v %v", other, err)
		}
	})
	t.Run("concurrent invalid retry", func(t *testing.T) {
		s, _ := factory(t)
		const n = 32
		type result struct {
			out      store.DeviceCreateOutcome
			inserted bool
			err      error
		}
		results := make(chan result, n)
		for range n {
			go func() {
				o, inserted, err := s.RecordDeviceCreateNotCreated(ctx, "a", "invalid", "hash", "invalid")
				results <- result{o, inserted, err}
			}()
		}
		var first store.DeviceCreateOutcome
		winners := 0
		for range n {
			r := <-results
			if r.err != nil {
				t.Fatal(r.err)
			}
			if first.Status == "" {
				first = r.out
			}
			if !reflect.DeepEqual(first, r.out) {
				t.Fatalf("different outcomes: %+v %+v", first, r.out)
			}
			if r.inserted {
				winners++
			}
		}
		if winners != 1 || first.Reason != "invalid" {
			t.Fatalf("winners=%d outcome=%+v", winners, first)
		}
	})
	t.Run("concurrent retry", func(t *testing.T) {
		s, devices := factory(t)
		const n = 32
		type result struct {
			outcome store.DeviceCreateOutcome
			created bool
			err     error
		}
		results := make(chan result, n)
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				o, c, e := s.CreateDeviceWithOutcome(ctx, "a", "id", "hash", store.Device{Endpoint: "one"})
				results <- result{o, c, e}
			}()
		}
		wg.Wait()
		close(results)
		var first store.DeviceCreateOutcome
		count := 0
		for r := range results {
			if r.err != nil {
				t.Fatal(r.err)
			}
			if first.Status == "" {
				first = r.outcome
			}
			if !reflect.DeepEqual(first, r.outcome) {
				t.Fatalf("different outcomes: %+v %+v", first, r.outcome)
			}
			if r.created {
				count++
			}
		}
		list, _, err := devices.List("a", store.DeviceFilter{}, "", 500)
		if err != nil || count != 1 || first.Status != "committed" || len(list) != 1 {
			t.Fatalf("winners=%d outcome=%+v devices=%+v error=%v", count, first, list, err)
		}
	})
}
