package main

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestDaemonUsesDeviceSecurityStore(t *testing.T) {
	devices := &memory.Devices{}
	srv, _, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), apiv1.Keys{}, devices)
	defer executor.Close()
	defer dispatcher.Close()
	defer srv.Close()
	err := devices.Security().Put(server.SecurityInfo{Endpoint: "wiring", PSKIdentity: "wiring", PSKKey: []byte("0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Security().ByEndpoint("wiring"); !ok {
		t.Fatal("server security store is not the device store security")
	}
}

func TestAtomicSecurityRejectsMismatchedStores(t *testing.T) {
	d := &memory.Devices{}
	if err := validateAtomicSecurity(d, server.NewMemorySecurityStore()); err == nil {
		t.Fatal("accepted unrelated security store")
	}
	if err := validateAtomicSecurity(d, (&memory.Devices{}).Security()); err == nil {
		t.Fatal("accepted another device store")
	}
	if err := validateAtomicSecurity(d, d.Security()); err != nil {
		t.Fatalf("rejected owned store: %v", err)
	}
}

func TestAtomicSecuritySQLiteOwnership(t *testing.T) {
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "one.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	other, err := sqlite.Open(filepath.Join(t.TempDir(), "two.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := validateAtomicSecurity(d, d.Security()); err != nil {
		t.Fatal(err)
	}
	if err := validateAtomicSecurity(d, coap.NewRevokingSecurityStore(d.Security(), func(string) {})); err != nil {
		t.Fatal(err)
	}
	if err := validateAtomicSecurity(d, other.Security()); err == nil {
		t.Fatal("accepted different database")
	}
}
