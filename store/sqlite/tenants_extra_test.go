package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestTenantsConcurrentUpdate(t *testing.T) {
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "tenants.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := d.Tenants()
	if !s.Create(apiv1.Tenant{ID: "counter"}) {
		t.Fatal("create")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				_, ok, valid := s.Update("counter", func(t *apiv1.Tenant) bool { t.Limits.Devices++; return true })
				if !ok || !valid {
					t.Error("update")
				}
			}
		}()
	}
	wg.Wait()
	if got, ok := s.Get("counter"); !ok || got.Limits.Devices != 200 {
		t.Fatalf("lost update: %#v", got)
	}
}
func TestTenantsUpgradeVersion10(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE tenants; PRAGMA user_version=10"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got, ok := d.Tenants().Get("default"); !ok || got.ID != "default" {
		t.Fatalf("upgrade default: %#v %v", got, ok)
	}
}
func TestTenantsClosedDatabaseFailsLoudly(t *testing.T) {
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Tenants()
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func(){"get": func() { s.Get("x") }, "list": func() { s.List() }, "create": func() { s.Create(apiv1.Tenant{}) }, "update": func() { s.Update("x", func(*apiv1.Tenant) bool { return true }) }, "delete": func() { s.Delete("x") }} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("database failure silently accepted")
				}
			}()
			call()
		})
	}
}
