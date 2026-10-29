package storetest

import (
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/model"
)

// Models exercises the tenant-scoped model store contract.
func Models(t *testing.T, fresh func() apiv1.ModelStore) {
	t.Helper()
	t.Run("TenantVersionReplacementAndDelete", func(t *testing.T) {
		s := fresh()
		one := &model.Object{ID: 5000, Name: "one", Version: model.Version{Major: 1}, Resources: map[uint16]*model.Resource{3: {ID: 3, Name: "temperature", Operations: model.OpRead, Units: "C"}}}
		two := &model.Object{ID: 5000, Name: "two", Version: model.Version{Major: 2}}
		other := &model.Object{ID: 5000, Name: "other tenant", Version: one.Version}
		s.Put("a", one)
		s.Put("a", two)
		s.Put("b", other)
		if len(s.List("missing")) != 0 {
			t.Fatal("unexpected tenant data")
		}
		if got := s.List("b"); len(got) != 1 || !reflect.DeepEqual(got[0], other) {
			t.Fatalf("tenant b: %#v", got)
		}
		replacement := *one
		replacement.Name = "replacement"
		s.Put("a", &replacement)
		got := s.List("a")
		if len(got) != 2 {
			t.Fatalf("replacement duplicated: %#v", got)
		}
		found := false
		for _, object := range got {
			if object.Version == one.Version {
				found = reflect.DeepEqual(object, &replacement)
			}
		}
		if !found {
			t.Fatalf("replacement not saved: %#v", got)
		}
		if !s.Delete("a", 5000, one.Version) || s.Delete("a", 5000, one.Version) {
			t.Fatal("delete result")
		}
		if got := s.List("a"); len(got) != 1 || !reflect.DeepEqual(got[0], two) {
			t.Fatalf("remaining versions: %#v", got)
		}
		if got := s.List("b"); len(got) != 1 || !reflect.DeepEqual(got[0], other) {
			t.Fatalf("cross-tenant deletion: %#v", got)
		}
	})
}
