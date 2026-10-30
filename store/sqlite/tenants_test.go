package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"testing"
)

func TestTenants(t *testing.T) {
	storetest.Tenants(t, func() apiv1.TenantStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "tenants.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Tenants()
	})
}
func TestTenantsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := apiv1.Tenant{ID: "alpha", Name: "Alpha", Status: "suspended", Limits: apiv1.TenantLimits{Devices: 123, TimeseriesRetentionDays: 30}}
	if !d.Tenants().Create(want) || !d.Tenants().Delete("default") {
		t.Fatal("setup")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got, ok := d.Tenants().Get("alpha"); !ok || got != want {
		t.Fatalf("reopen: %#v %v", got, ok)
	}
	if _, ok := d.Tenants().Get("default"); ok {
		t.Fatal("deleted default recreated")
	}
}
