package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

type deviceSecurityCreator interface {
	CreateDeviceWithSecurity(context.Context, string, string, string, store.Device, server.SecurityInfo) (store.DeviceCreateOutcome, bool, error)
}

// DeviceSecurity exercises atomic credential provisioning through the shared store.
func DeviceSecurity(t *testing.T, devices store.Devices, security server.SecurityStore) {
	t.Helper()
	creator, ok := devices.(deviceSecurityCreator)
	if !ok {
		t.Fatal("device store lacks atomic security create")
	}
	ctx := context.Background()
	outcomes := devices.(store.DeviceCreateStore)
	fence, err := outcomes.GetOrFenceDeviceCreate(ctx, "a", "fence")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range []server.SecurityInfo{
		{Endpoint: "fenced-device", PublicKey: []byte("unsupported")},
		{Endpoint: "mismatch", PublicKey: []byte("unsupported")},
	} {
		got, inserted, err := creator.CreateDeviceWithSecurity(ctx, "a", "fence", "anything", store.Device{Endpoint: "fenced-device"}, sec)
		if err != nil || inserted || !reflect.DeepEqual(got, fence) {
			t.Errorf("fence replay: got %+v inserted=%v err=%v, want %+v", got, inserted, err, fence)
		}
	}
	d := store.Device{Endpoint: "pump"}
	good := server.SecurityInfo{Endpoint: "pump", PSKIdentity: "good", PSKKey: []byte("good-key")}
	out, inserted, err := creator.CreateDeviceWithSecurity(ctx, "a", "op", "fp", d, good)
	if err != nil || !inserted || out.Status != "committed" {
		t.Fatalf("create: %+v %v %v", out, inserted, err)
	}
	out, inserted, err = creator.CreateDeviceWithSecurity(ctx, "a", "op", "fp", d, good)
	if err != nil || inserted || out.Status != "committed" {
		t.Fatalf("replay: %+v %v %v", out, inserted, err)
	}
	invalidReplay := server.SecurityInfo{Endpoint: "wrong", PublicKey: []byte("unsupported")}
	if got, inserted, err := creator.CreateDeviceWithSecurity(ctx, "a", "op", "fp", d, invalidReplay); err != nil || inserted || !reflect.DeepEqual(got, out) {
		t.Errorf("committed replay: %+v %v %v", got, inserted, err)
	}
	if _, inserted, err := creator.CreateDeviceWithSecurity(ctx, "a", "op", "changed", d, invalidReplay); inserted || !errors.Is(err, store.ErrOperationIDReused) {
		t.Errorf("invalid reused operation: inserted=%v err=%v", inserted, err)
	}
	bad := server.SecurityInfo{Endpoint: "pump", PSKIdentity: "attacker", PSKKey: []byte("bad-key")}
	out, _, err = creator.CreateDeviceWithSecurity(ctx, "b", "op", "other", d, bad)
	if err != nil || out.Reason != "endpoint-conflict" {
		t.Fatalf("conflict: %+v %v", out, err)
	}
	if _, ok := security.ByPSKIdentity("attacker"); ok {
		t.Fatal("attacker credential installed")
	}
	got, ok := security.ByEndpoint("pump")
	if !ok || got.PSKIdentity != "good" || string(got.PSKKey) != "good-key" {
		t.Fatalf("original credential changed: %+v", got)
	}
	if _, err := devices.Get("a", "pump"); err != nil {
		t.Fatal(err)
	}
	if _, err := devices.Get("b", "pump"); err == nil {
		t.Fatal("cross-tenant device created")
	}
	if _, _, err := creator.CreateDeviceWithSecurity(ctx, "a", "op", "changed", d, good); !errors.Is(err, store.ErrOperationIDReused) {
		t.Fatalf("reused operation: %v", err)
	}
	_, _, err = creator.CreateDeviceWithSecurity(ctx, "b", "duplicate", "fp", store.Device{Endpoint: "other"}, server.SecurityInfo{Endpoint: "other", PSKIdentity: "good", PSKKey: []byte("other")})
	if err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := devices.Get("b", "other"); !errors.Is(err, store.ErrDeviceNotFound) {
		t.Fatalf("duplicate identity left device: %v", err)
	}
	if _, err := devices.(store.DeviceCreateStore).GetDeviceCreate(ctx, "b", "duplicate"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("duplicate identity left outcome: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan store.DeviceCreateOutcome, 2)
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, tenant := range []string{"x", "y"} {
		wg.Add(1)
		go func(tenant string) {
			defer wg.Done()
			<-start
			o, _, err := creator.CreateDeviceWithSecurity(ctx, tenant, "race", tenant, store.Device{Endpoint: "race"}, server.SecurityInfo{Endpoint: "race", PSKIdentity: tenant, PSKKey: []byte(tenant)})
			results <- o
			errs <- err
		}(tenant)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("race: %v", err)
		}
	}
	committed, conflicts := 0, 0
	for o := range results {
		if o.Status == "committed" {
			committed++
		}
		if o.Reason == "endpoint-conflict" {
			conflicts++
		}
	}
	if committed != 1 || conflicts != 1 {
		t.Fatalf("race: committed=%d conflict=%d", committed, conflicts)
	}
	_, x := security.ByPSKIdentity("x")
	_, y := security.ByPSKIdentity("y")
	if x == y {
		t.Fatalf("race credentials: x=%v y=%v", x, y)
	}
}
