package main

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestRecoverObservationsIncludesOrphansAndOnlyDefaultTenant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	records := db.Observations()
	cases := []struct {
		tenant, id, status string
		persistent         bool
		want               string
	}{
		{"default", "active", "active", false, "ended"},
		{"default", "pending", "pending", false, "ended"},
		{"default", "failed", "failed", false, "ended"},
		{"default", "persistent", "active", true, "active"},
		{"other", "other-tenant", "active", false, "active"},
	}
	for _, tc := range cases {
		if err := records.Create(tc.tenant, store.Observation{Tenant: tc.tenant, ID: tc.id, Device: "deleted-device", Persistent: tc.persistent, Status: tc.status}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := recoverObservations(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range cases {
		got, err := records.Get(tc.tenant, tc.id)
		if err != nil || got.Status != tc.want {
			t.Errorf("%s: status %q, error %v; want %q", tc.id, got.Status, err, tc.want)
		}
	}
}
