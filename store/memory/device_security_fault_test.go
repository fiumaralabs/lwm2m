package memory

import (
	"context"
	"errors"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"testing"
)

func TestDeviceSecurityFault(t *testing.T) {
	d := &Devices{}
	for _, point := range []string{"after-device", "after-security"} {
		t.Run(point, func(t *testing.T) {
			d.deviceSecurityHook = func(at string) error {
				if at == point {
					return errors.New("injected")
				}
				return nil
			}
			dev := store.Device{Endpoint: point}
			sec := server.SecurityInfo{Endpoint: point, PSKIdentity: point, PSKKey: []byte("key")}
			_, _, err := d.CreateDeviceWithSecurity(context.Background(), "a", point, point, dev, sec)
			if err == nil {
				t.Fatal("fault ignored")
			}
			d.deviceSecurityHook = nil
			if _, err := d.Get("a", point); !errors.Is(err, store.ErrDeviceNotFound) {
				t.Fatalf("device remains: %v", err)
			}
			if _, ok := d.Security().ByEndpoint(point); ok {
				t.Fatal("credential remains")
			}
			if _, err := d.GetDeviceCreate(context.Background(), "a", point); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("outcome remains: %v", err)
			}
			out, inserted, err := d.CreateDeviceWithSecurity(context.Background(), "a", point, point, dev, sec)
			if err != nil || !inserted || out.Status != "committed" {
				t.Fatalf("retry: %+v %v %v", out, inserted, err)
			}
		})
	}
}
