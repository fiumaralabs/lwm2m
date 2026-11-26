package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

func TestDeviceBootstrapSQLiteWriteFailure(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.db.Exec(`CREATE TRIGGER fail_config BEFORE INSERT ON bootstrap_configs BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.PutBootstrapConfig(ctx, "a", "other", []byte(`{}`)); err == nil {
		t.Fatal("write failure was not returned")
	}
	_, _, err = d.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "op", "fp", store.Device{Endpoint: "pump"}, server.SecurityInfo{Endpoint: "pump", PSKIdentity: "id", PSKKey: []byte("key")}, []byte(`{}`))
	if err == nil {
		t.Fatal("create ignored write failure")
	}
	if _, err := d.Get("a", "pump"); !errors.Is(err, store.ErrDeviceNotFound) {
		t.Fatalf("device remains: %v", err)
	}
	if _, ok := d.Security().ByEndpoint("pump"); ok {
		t.Fatal("credential remains")
	}
	if _, ok, err := d.GetBootstrapConfig(ctx, "a", "pump"); err != nil || ok {
		t.Fatalf("config remains: %v %v", ok, err)
	}
	if _, err := d.GetDeviceCreate(ctx, "a", "op"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outcome remains: %v", err)
	}
}
