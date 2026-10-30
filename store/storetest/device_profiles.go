package storetest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// DeviceProfiles checks the shared tenant-isolated profile store contract.
func DeviceProfiles(t *testing.T, newStore func() apiv1.DeviceProfileStore) {
	t.Helper()
	t.Run("CreateGetAndObservations", func(t *testing.T) {
		s := newStore()
		p := apiv1.DeviceProfile{ID: "b", Name: "profile", Observations: json.RawMessage(`[{"path":"/3/0/1","pmin":10}]`), Attributes: json.RawMessage(`{"pmax":60}`), OnRegister: json.RawMessage(`{"read":["/3/0"]}`), Timeseries: json.RawMessage(`{"enabled":true}`), BootstrapTemplate: json.RawMessage(`{"name":"x"}`)}
		if err := s.Create("a", p); err != nil {
			t.Fatal(err)
		}
		if err := s.Create("a", p); err == nil {
			t.Fatal("duplicate accepted")
		}
		got, ok := s.Get("a", "b")
		if !ok || !reflect.DeepEqual(got, p) {
			t.Fatalf("got %#v, %v", got, ok)
		}
		p.Observations[0] = 'x'
		got.Attributes[0] = 'x'
		again, _ := s.Get("a", "b")
		if string(again.Observations) != `[{"path":"/3/0/1","pmin":10}]` || string(again.Attributes) != `{"pmax":60}` {
			t.Fatal("store did not retain owned snapshot")
		}
	})
	t.Run("ListOrderAndTenantIsolation", func(t *testing.T) {
		s := newStore()
		for _, id := range []string{"c", "a", "b"} {
			if err := s.Create("one", apiv1.DeviceProfile{ID: id}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Create("two", apiv1.DeviceProfile{ID: "a", Name: "other"}); err != nil {
			t.Fatal(err)
		}
		got := s.List("one")
		if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
			t.Fatalf("list: %#v", got)
		}
		if _, ok := s.Get("two", "b"); ok {
			t.Fatal("foreign get")
		}
		if s.Replace("two", apiv1.DeviceProfile{ID: "b"}) {
			t.Fatal("foreign replace")
		}
		if found, _ := s.Delete("two", "b"); found {
			t.Fatal("foreign delete")
		}
		if len(s.List("missing")) != 0 {
			t.Fatal("foreign list")
		}
	})
	t.Run("ReplaceAndDelete", func(t *testing.T) {
		s := newStore()
		p := apiv1.DeviceProfile{ID: "a", Name: "original"}
		if s.Replace("t", p) {
			t.Fatal("replace created missing item")
		}
		if err := s.Create("t", p); err != nil {
			t.Fatal(err)
		}
		p.Name = "changed"
		if !s.Replace("t", p) {
			t.Fatal("replace missing")
		}
		if got, ok := s.Get("t", "a"); !ok || got.Name != "changed" {
			t.Fatalf("replace: %#v", got)
		}
		if found, inUse := s.Delete("t", "a"); !found || inUse {
			t.Fatalf("delete: %v %v", found, inUse)
		}
		if found, inUse := s.Delete("t", "a"); found || inUse {
			t.Fatalf("delete missing: %v %v", found, inUse)
		}
	})
}
