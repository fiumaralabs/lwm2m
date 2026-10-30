package storetest

import (
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Firmware exercises the tenant-isolated firmware metadata contract.
func Firmware(t *testing.T, fresh func() apiv1.FirmwareStore) {
	t.Helper()
	t.Run("IsolationReplacementAndSnapshots", func(t *testing.T) {
		s := fresh()
		f := apiv1.Firmware{ID: "z", Name: "original", Version: "1", Status: "pending", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Labels: map[string]string{"x": "y"}}
		if err := s.Create("a", f); err != nil {
			t.Fatal(err)
		}
		f.Labels["x"] = "mutated"
		if err := s.Create("b", apiv1.Firmware{ID: "z", Name: "other"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Create("a", apiv1.Firmware{ID: "a", Name: "first"}); err != nil {
			t.Fatal(err)
		}
		got := s.List("a")
		if len(got) != 2 || got[0].ID != "a" || got[1].Labels["x"] != "y" || len(s.List("missing")) != 0 {
			t.Fatalf("list: %#v", got)
		}
		got[1].Labels["x"] = "changed"
		if v, ok := s.Get("a", "z"); !ok || v.Labels["x"] != "y" {
			t.Fatalf("get: %#v %v", v, ok)
		}
		labels := map[string]string{"new": "label"}
		name := "new"
		desc := "description"
		patched, ok := s.Patch("a", "z", apiv1.FirmwarePatch{Name: &name, Description: &desc, Labels: &labels})
		if !ok || patched.Name != name || patched.Description != desc || !reflect.DeepEqual(patched.Labels, labels) || patched.Version != "1" {
			t.Fatalf("patch: %#v %v", patched, ok)
		}
		labels["new"] = "changed"
		patched.Labels["new"] = "changed"
		if v, _ := s.Get("a", "z"); v.Labels["new"] != "label" {
			t.Fatal("patch aliased")
		}
		if _, ok := s.Patch("missing", "z", apiv1.FirmwarePatch{}); ok {
			t.Fatal("cross-tenant patch")
		}
		if found, used := s.Delete("a", "z"); !found || used {
			t.Fatal("delete")
		}
		if found, used := s.Delete("a", "z"); found || used {
			t.Fatal("repeat delete")
		}
		if v, ok := s.Get("b", "z"); !ok || v.Name != "other" {
			t.Fatal("cross-tenant delete")
		}
		if err := s.Create("a", apiv1.Firmware{ID: "a", Name: "replacement"}); err != nil {
			t.Fatal(err)
		}
		if v, ok := s.Get("a", "a"); !ok || v.Name != "replacement" || v.Labels != nil {
			t.Fatal("replacement")
		}
		empty := map[string]string{}
		if v, ok := s.Patch("a", "a", apiv1.FirmwarePatch{Labels: &empty}); !ok || v.Labels == nil {
			t.Fatal("empty labels lost")
		}
		if v, ok := s.Get("a", "a"); !ok || v.Labels == nil {
			t.Fatal("empty labels lost on read")
		}
	})
}
