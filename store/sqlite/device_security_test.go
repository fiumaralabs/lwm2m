package sqlite

import (
	"context"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"testing"
)

func TestDeviceSecurityReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dev := store.Device{Endpoint: "pump"}
	sec := server.SecurityInfo{Endpoint: "pump", PSKIdentity: "id", PSKKey: []byte("key")}
	_, _, err = d.CreateDeviceWithSecurity(context.Background(), "a", "op", "fp", dev, sec)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	out, inserted, err := d.CreateDeviceWithSecurity(context.Background(), "a", "op", "fp", dev, sec)
	if err != nil || inserted || out.Status != "committed" {
		t.Fatalf("replay: %+v %v %v", out, inserted, err)
	}
	if _, err := d.Get("a", "pump"); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.Security().ByPSKIdentity("id"); !ok || string(got.PSKKey) != "key" {
		t.Fatal("credential not persisted")
	}
}

func TestDeviceSecurityFault(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	storetest.DeviceSecurityFault(t, d, d.Security(), func(h func(string) error) { d.deviceCreateHook = h })
}

func TestDeviceSecurityContract(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	storetest.DeviceSecurity(t, d, d.Security())
}

func TestDeviceBootstrapContract(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	storetest.DeviceBootstrap(t, d, d.Security(), func(h func(string) error) { d.deviceCreateHook = h })
}
