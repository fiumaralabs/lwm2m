package storetest

import (
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Tenants exercises the tenant store contract shared by memory and SQLite.
func Tenants(t *testing.T, fresh func() apiv1.TenantStore) {
	t.Helper()
	t.Run("CRUDAndDefault", func(t *testing.T) {
		s := fresh()
		def := apiv1.Tenant{ID: "default", Name: "default", Status: "active"}
		if got, ok := s.Get("default"); !ok || got != def {
			t.Fatalf("default: %#v %v", got, ok)
		}
		a := apiv1.Tenant{ID: "a", Name: "A", Status: "active", Limits: apiv1.TenantLimits{Devices: 5, OperationsPerMinute: 6, EventsPerSecond: 7, FirmwareStorageBytes: 8, TimeseriesRetentionDays: 9}}
		z := apiv1.Tenant{ID: "z", Name: "Z", Status: "active"}
		if !s.Create(z) || !s.Create(a) || s.Create(a) || s.Create(def) {
			t.Fatal("create or duplicate")
		}
		if got, ok := s.Get("a"); !ok || got != a {
			t.Fatalf("get: %#v %v", got, ok)
		}
		if got := s.List(); !reflect.DeepEqual(got, []apiv1.Tenant{a, def, z}) {
			t.Fatalf("list: %#v", got)
		}
		updated, found, valid := s.Update("a", func(x *apiv1.Tenant) bool { x.Status = "suspended"; x.Limits.Devices = 42; return true })
		a.Status = "suspended"
		a.Limits.Devices = 42
		if !found || !valid || updated != a {
			t.Fatalf("update: %#v %v %v", updated, found, valid)
		}
		if got, ok := s.Get("a"); !ok || got != a {
			t.Fatalf("stored update: %#v", got)
		}
		rejected, found, valid := s.Update("a", func(x *apiv1.Tenant) bool { x.Name = "rejected"; return false })
		if !found || valid || rejected.Name != "rejected" {
			t.Fatal("rejected update result")
		}
		if got, _ := s.Get("a"); got != a {
			t.Fatal("rejected update persisted")
		}
		called := false
		_, found, valid = s.Update("missing", func(*apiv1.Tenant) bool { called = true; return true })
		if found || valid || called {
			t.Fatal("missing update")
		}
		if !s.Delete("a") || s.Delete("a") {
			t.Fatal("delete")
		}
		if _, ok := s.Get("a"); ok {
			t.Fatal("deleted tenant found")
		}
		if !s.Delete("default") {
			t.Fatal("store permits deleting default; handler protects it")
		}
	})
}
