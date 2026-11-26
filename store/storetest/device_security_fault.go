package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

func DeviceSecurityFault(t *testing.T, devices store.Devices, security server.SecurityStore, setHook func(func(string) error)) {
	t.Helper()
	creator := devices.(store.DeviceSecurityCreateStore)
	outcomes := devices.(store.DeviceCreateStore)
	ctx := context.Background()
	for _, point := range []string{"after-device", "after-security"} {
		t.Run(point, func(t *testing.T) {
			setHook(func(at string) error {
				if at == point {
					return errors.New("injected")
				}
				return nil
			})
			d := store.Device{Endpoint: point}
			sec := server.SecurityInfo{Endpoint: point, PSKIdentity: point, PSKKey: []byte("key")}
			_, _, err := creator.CreateDeviceWithSecurity(ctx, "a", point, point, d, sec)
			if err == nil {
				t.Fatal("injected failure was ignored")
			}
			setHook(nil)
			if _, err := devices.Get("a", point); !errors.Is(err, store.ErrDeviceNotFound) {
				t.Fatalf("device remains: %v", err)
			}
			if _, ok := security.ByEndpoint(point); ok {
				t.Fatal("credential remains")
			}
			if _, err := outcomes.GetDeviceCreate(ctx, "a", point); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("outcome remains: %v", err)
			}
			out, inserted, err := creator.CreateDeviceWithSecurity(ctx, "a", point, point, d, sec)
			if err != nil || !inserted || out.Status != "committed" {
				t.Fatalf("retry: %+v %v %v", out, inserted, err)
			}
		})
	}
}
