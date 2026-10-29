package storetest

import (
	"encoding/json"
	"errors"
	"github.com/fiumaralabs/lwm2m/store"
	"sync"
	"testing"
	"time"
)

// Observations is the shared persistence contract, including owned snapshots.
func Observations(t *testing.T, fresh func() store.Observations) {
	t.Helper()
	t.Run("RawJSONPreservation", func(t *testing.T) {
		s := fresh()
		v := store.Observation{Tenant: "a", ID: "raw", Device: "pump", Attributes: map[string]json.RawMessage{"value": json.RawMessage(` { "x" : 1 } `), "nil": nil}, Error: json.RawMessage(` { "message" : "oops" } `)}
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get("a", "raw")
		if err != nil || string(got.Attributes["value"]) != string(v.Attributes["value"]) || got.Attributes["nil"] != nil || string(got.Error) != string(v.Error) {
			t.Fatalf("raw JSON changed: %+v %v", got, err)
		}
	})
	t.Run("DeliveryState", func(t *testing.T) {
		s := fresh()
		d, ok := s.(interface {
			UpdateDelivery(string, string, func(*store.Observation)) (store.Observation, error)
		})
		if !ok {
			t.Fatal("delivery updates not supported")
		}
		v := store.Observation{Tenant: "a", ID: "id", Device: "pump", Status: "pending", Source: "api"}
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(123, 0).UTC()
		got, err := d.UpdateDelivery("a", "id", func(v *store.Observation) {
			v.Status = "active"
			v.EstablishedAt = &at
			v.NotificationCount = 2
		})
		if err != nil || got.Status != "active" || got.NotificationCount != 2 || !got.EstablishedAt.Equal(at) {
			t.Fatalf("delivery: %+v %v", got, err)
		}
		if err := s.End("a", "id"); err != nil {
			t.Fatal(err)
		}
		called := false
		got, err = d.UpdateDelivery("a", "id", func(v *store.Observation) { called = true; v.Status = "active" })
		if err != nil || called || got.Status != "ended" {
			t.Fatalf("ended delivery: %+v %v called=%v", got, err, called)
		}
		if _, err := d.UpdateDelivery("b", "id", func(*store.Observation) {}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("isolated delivery: %v", err)
		}
	})
	t.Run("ConcurrentEndAndAttributes", func(t *testing.T) {
		s := fresh()
		for range 50 {
			// Repeated writes to one record must never resurrect ended intent.
			id := time.Now().String()
			v := store.Observation{Tenant: "a", ID: id, Device: "pump", Status: "pending", Source: "api"}
			if err := s.Create("a", v); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, end := range []bool{true, false} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if end {
						if err := s.End("a", id); err != nil {
							t.Error(err)
						}
					} else {
						if _, err := s.UpdateAttributes("a", id, map[string]json.RawMessage{"pmin": json.RawMessage(`1`)}); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			got, err := s.Get("a", id)
			if err != nil || got.Status != "ended" || string(got.Attributes["pmin"]) != "1" {
				t.Fatalf("lost mutation: %+v %v", got, err)
			}
		}
	})
	t.Run("MetadataOwnershipAndOrdering", func(t *testing.T) {
		s := fresh()
		at := time.Unix(123, 0).UTC()
		v := store.Observation{Tenant: "a", ID: "z", Device: "pump", EstablishedAt: &at, LastNotificationAt: &at, Error: json.RawMessage(`{"code":"x"}`)}
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		at = time.Unix(999, 0)
		v.Error[9] = 'y'
		got, err := s.Get("a", "z")
		if err != nil || got.EstablishedAt.Unix() != 123 || got.LastNotificationAt.Unix() != 123 || string(got.Error) != `{"code":"x"}` {
			t.Fatalf("metadata input alias: %+v %v", got, err)
		}
		*got.EstablishedAt = at
		*got.LastNotificationAt = at
		got.Error[9] = 'z'
		got, _ = s.Get("a", "z")
		if got.EstablishedAt.Unix() != 123 || got.LastNotificationAt.Unix() != 123 || string(got.Error) != `{"code":"x"}` {
			t.Fatal("metadata output alias")
		}
		v.ID = "a"
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		v.Tenant = "b"
		if err := s.Create("b", v); err != nil {
			t.Fatal(err)
		}
		xs, err := s.List("a", "pump")
		if err != nil || len(xs) != 2 || xs[0].ID != "a" || xs[1].ID != "z" {
			t.Fatalf("order/isolation: %+v %v", xs, err)
		}
	})
	t.Run("AtomicMutations", func(t *testing.T) {
		s := fresh()
		v := store.Observation{Tenant: "a", ID: "id", Device: "pump", Paths: []string{"/3/0"}, Status: "active", Source: "api", Persistent: true, NotificationCount: 7}
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		a := map[string]json.RawMessage{"pmin": json.RawMessage(`20`)}
		updated, err := s.UpdateAttributes("a", "id", a)
		if err != nil || updated.ID != v.ID || updated.Status != "active" || updated.NotificationCount != 7 {
			t.Fatalf("update: %+v %v", updated, err)
		}
		a["pmin"][0] = '9'
		updated.Attributes["pmin"][0] = '8'
		if err := s.End("b", "id"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("isolated end: %v", err)
		}
		if _, err := s.UpdateAttributes("b", "id", a); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("isolated patch: %v", err)
		}
		if err := s.End("a", "id"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get("a", "id")
		if got.Status != "ended" || got.NotificationCount != 7 || string(got.Attributes["pmin"]) != "20" {
			t.Fatalf("end: %+v", got)
		}
		v.ID = "profile"
		v.Source = "profile"
		_ = s.Create("a", v)
		if err := s.End("a", "profile"); !errors.Is(err, store.ErrObservationProfile) {
			t.Fatalf("profile end: %v", err)
		}
		got, _ = s.Get("a", "profile")
		if got.Status != "active" {
			t.Fatal("profile changed")
		}
	})
	t.Run("CreateGetListOwnership", func(t *testing.T) {
		s := fresh()
		v := store.Observation{ID: "one", Tenant: "a", Device: "pump", Paths: []string{"/3/0"}, Attributes: map[string]json.RawMessage{"pmin": json.RawMessage(`10`)}, Status: "pending", Source: "api", Persistent: true}
		if err := s.Create("b", v); !errors.Is(err, store.ErrTenant) {
			t.Fatalf("tenant: %v", err)
		}
		if err := s.Create("a", v); err != nil {
			t.Fatal(err)
		}
		if err := s.Create("a", v); !errors.Is(err, store.ErrExists) {
			t.Fatalf("duplicate: %v", err)
		}
		v.Paths[0] = "/9"
		v.Attributes["pmin"][0] = '9'
		got, err := s.Get("a", "one")
		if err != nil || got.Paths[0] != "/3/0" || string(got.Attributes["pmin"]) != "10" {
			t.Fatalf("snapshot: %+v %v", got, err)
		}
		got.Paths[0] = "/8"
		got.Attributes["pmin"][0] = '8'
		list, err := s.List("a", "pump")
		if err != nil || len(list) != 1 || list[0].Paths[0] != "/3/0" || string(list[0].Attributes["pmin"]) != "10" {
			t.Fatalf("list: %+v %v", list, err)
		}
		list[0].Paths[0] = "/7"
		list[0].Attributes["pmin"][0] = '7'
		got, _ = s.Get("a", "one")
		if got.Paths[0] != "/3/0" || string(got.Attributes["pmin"]) != "10" {
			t.Fatal("list aliases store")
		}
		for _, filter := range [][2]string{{"b", "pump"}, {"a", "other"}} {
			xs, e := s.List(filter[0], filter[1])
			if e != nil || len(xs) != 0 || xs == nil {
				t.Fatalf("filtered list: %v %v", xs, e)
			}
		}
		if _, err := s.Get("b", "one"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("isolated get: %v", err)
		}
	})
}
