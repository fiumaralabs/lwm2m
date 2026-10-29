package sqlite_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestAuditStoreContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) apiv1.AuditStore
	}{
		{"memory", func(t *testing.T) apiv1.AuditStore { return &apiv1.MemoryAuditStore{} }},
		{"sqlite", func(t *testing.T) apiv1.AuditStore {
			d, err := sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			return d.Audit()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.open(t)
			a := apiv1.AuditEntry{ID: "same", Time: time.Date(2024, 1, 1, 0, 0, 0, 123, time.UTC), Actor: apiv1.AuditActor{Kind: "apikey", ID: "id", Name: "name"}, Action: "create", Resource: "/v1/devices", Status: 201, IP: "127.0.0.1", Summary: map[string]string{"x": "original"}}
			b := a
			b.ID = "second"
			b.Time = a.Time.Add(-time.Hour)
			b.Summary = nil
			if err := s.Append("one", a); err != nil {
				t.Fatal(err)
			}
			a.Summary["x"] = "changed"
			if err := s.Append("two", b); err != nil {
				t.Fatal(err)
			}
			if err := s.Append("one", b); err != nil {
				t.Fatal(err)
			}
			got, err := s.List("one")
			if err != nil || len(got) != 2 || got[0].ID != "same" || got[1].ID != "second" || got[0].Summary["x"] != "original" || got[1].Summary != nil {
				t.Fatalf("list: %+v %v", got, err)
			}
			expected := a
			expected.Summary = map[string]string{"x": "original"}
			if !reflect.DeepEqual(got[0], expected) {
				t.Fatalf("entry: %+v", got[0])
			}
			got[0].Summary["x"] = "mutated"
			got, err = s.List("one")
			if err != nil || got[0].Summary["x"] != "original" {
				t.Fatalf("snapshot: %+v %v", got, err)
			}
			other, err := s.List("two")
			if err != nil || len(other) != 1 || other[0].ID != "second" {
				t.Fatalf("isolation: %+v %v", other, err)
			}
			empty, err := s.List("missing")
			if err != nil || len(empty) != 0 {
				t.Fatalf("empty: %+v %v", empty, err)
			}
		})
	}
}
