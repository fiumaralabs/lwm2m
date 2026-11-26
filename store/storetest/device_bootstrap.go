package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

type bootstrapCreator interface {
	CreateDeviceWithSecurityAndBootstrap(context.Context, string, string, string, store.Device, server.SecurityInfo, json.RawMessage) (store.DeviceCreateOutcome, bool, error)
	PutBootstrapConfig(context.Context, string, string, json.RawMessage) error
	GetBootstrapConfig(context.Context, string, string) (json.RawMessage, bool, error)
	DeleteBootstrapConfig(context.Context, string, string) (bool, error)
}

func DeviceBootstrap(t *testing.T, devices store.Devices, security server.SecurityStore, hook func(func(string) error)) {
	t.Helper()
	s, ok := devices.(bootstrapCreator)
	if !ok {
		t.Fatal("atomic bootstrap create store not implemented")
	}
	ctx := context.Background()
	outOnly, insertedOnly, errOnly := s.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "only-op", "fp", store.Device{Endpoint: "only"}, server.SecurityInfo{}, json.RawMessage(`{}`))
	if errOnly != nil || !insertedOnly || outOnly.Status != "committed" {
		t.Fatalf("bootstrap-only create: %+v %v %v", outOnly, insertedOnly, errOnly)
	}
	if _, ok := security.ByEndpoint("only"); ok {
		t.Fatal("bootstrap-only create wrote a credential")
	}
	if err := s.PutBootstrapConfig(ctx, "a", "nil", nil); err == nil {
		t.Fatal("nil configuration accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.PutBootstrapConfig(canceled, "a", "canceled", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	if err := s.PutBootstrapConfig(ctx, "a", "orphan", json.RawMessage(`{"old":true}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "orphan-op", "fp", store.Device{Endpoint: "orphan"}, server.SecurityInfo{Endpoint: "orphan", PSKIdentity: "orphan", PSKKey: []byte("key")}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("existing config overwritten")
	}
	if _, err := devices.Get("a", "orphan"); !errors.Is(err, store.ErrDeviceNotFound) {
		t.Fatalf("orphan device: %v", err)
	}
	if _, ok := security.ByEndpoint("orphan"); ok {
		t.Fatal("orphan credential persisted")
	}
	original := json.RawMessage(`{"owner":"b"}`)
	if err := s.PutBootstrapConfig(ctx, "b", "pump", original); err != nil {
		t.Fatal(err)
	}
	d := store.Device{Endpoint: "pump"}
	sec := server.SecurityInfo{Endpoint: "pump", PSKIdentity: "id", PSKKey: []byte("key")}
	points := []string{}
	if hook != nil {
		points = []string{"after-device", "after-security", "after-bootstrap"}
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			hook(func(p string) error {
				if p == point {
					return errors.New("injected")
				}
				return nil
			})
			_, _, err := s.CreateDeviceWithSecurityAndBootstrap(ctx, "a", point, point, d, sec, json.RawMessage(`{"owner":"a"}`))
			hook(nil)
			if err == nil {
				t.Fatal("injected error ignored")
			}
			if _, err := devices.Get("a", "pump"); !errors.Is(err, store.ErrDeviceNotFound) {
				t.Fatalf("device remains: %v", err)
			}
			if _, ok := security.ByEndpoint("pump"); ok {
				t.Fatal("credential remains")
			}
			if _, ok, err := s.GetBootstrapConfig(ctx, "a", "pump"); err != nil || ok {
				t.Fatalf("config remains: %v %v", ok, err)
			}
			b, ok, err := s.GetBootstrapConfig(ctx, "b", "pump")
			if err != nil || !ok || string(b) != string(original) {
				t.Fatalf("other tenant changed: %s %v %v", b, ok, err)
			}
		})
	}
	input := json.RawMessage(`{"owner":"a"}`)
	out, inserted, err := s.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "op", "fp", d, sec, input)
	if err != nil || !inserted || out.Status != "committed" {
		t.Fatalf("create: %+v %v %v", out, inserted, err)
	}
	input[0] = 'x'
	b, ok, err := s.GetBootstrapConfig(ctx, "a", "pump")
	if err != nil || !ok || string(b) != `{"owner":"a"}` {
		t.Fatalf("config: %s %v %v", b, ok, err)
	}
	b[0] = 'x'
	b, _, _ = s.GetBootstrapConfig(ctx, "a", "pump")
	if b[0] != '{' {
		t.Fatal("read aliases storage")
	}
	out, inserted, err = s.CreateDeviceWithSecurityAndBootstrap(ctx, "a", "op", "fp", d, sec, json.RawMessage(`{}`))
	if err != nil || inserted || out.Status != "committed" {
		t.Fatalf("replay: %+v %v %v", out, inserted, err)
	}
	b, _, _ = s.GetBootstrapConfig(ctx, "b", "pump")
	if string(b) != string(original) {
		t.Fatal("other tenant changed")
	}
	if deleted, err := s.DeleteBootstrapConfig(ctx, "a", "pump"); err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if deleted, err := s.DeleteBootstrapConfig(ctx, "a", "pump"); err != nil || deleted {
		t.Fatalf("delete missing: %v %v", deleted, err)
	}
	if b, ok, err := s.GetBootstrapConfig(ctx, "b", "pump"); err != nil || !ok || string(b) != string(original) {
		t.Fatalf("other tenant changed after delete: %s %v %v", b, ok, err)
	}
}
