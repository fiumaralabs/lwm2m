package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

func TestDeviceBootstrapFault(t *testing.T) {
	for _, point := range []string{"after-device", "after-security", "after-bootstrap"} {
		t.Run(point, func(t *testing.T) {
			d := &Devices{}
			ctx := context.Background()
			if err := d.PutBootstrapConfig(ctx, "b", "pump", json.RawMessage(`{"b":true}`)); err != nil {
				t.Fatal(err)
			}
			d.deviceSecurityHook = func(p string) error {
				if p == point {
					return errors.New("fault")
				}
				return nil
			}
			_, _, err := d.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "op", "fp", store.Device{Endpoint: "pump"}, server.SecurityInfo{Endpoint: "pump", PSKIdentity: "id", PSKKey: []byte("key")}, json.RawMessage(`{}`))
			if err == nil {
				t.Fatal("fault ignored")
			}
			if _, err := d.Get("a", "pump"); !errors.Is(err, store.ErrDeviceNotFound) {
				t.Fatalf("device remains: %v", err)
			}
			if _, ok := d.Security().ByEndpoint("pump"); ok {
				t.Fatal("credential remains")
			}
			if _, ok, err := d.GetBootstrapConfig(ctx, "a", "pump"); ok || err != nil {
				t.Fatalf("bootstrap remains: %v %v", ok, err)
			}
			if b, ok, err := d.GetBootstrapConfig(ctx, "b", "pump"); !ok || err != nil || string(b) != `{"b":true}` {
				t.Fatalf("other tenant: %s %v", b, err)
			}
			if _, err := d.GetDeviceCreate(ctx, "a", "op"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("outcome remains: %v", err)
			}
		})
	}
}
