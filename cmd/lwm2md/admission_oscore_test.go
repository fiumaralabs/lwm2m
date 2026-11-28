package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

// Before ModeOSCORE, the admission hook rejected OSCORE's NoSec identity.
// This verifies an admitted OSCORE key remains stable across re-registration.
func TestAdmittedOSCOREReregistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	bindings, err := admission.OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.Close()
	devices, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer devices.Close()
	identity := server.Identity{Mode: server.ModeOSCORE, OSCOREIDContext: []byte{1}, OSCORESenderID: []byte{2}, OSCOREEndpoint: "device"}
	encoded, err := json.Marshal(struct {
		Mode       server.SecurityMode
		Endpoint   string
		Credential []byte
	}{server.ModeOSCORE, "device", []byte("01/02")})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	b := admission.Binding{ExchangeID: "first", TenantID: "default", IdentityID: hex.EncodeToString(digest[:]), Fingerprint: "first-fingerprint", DecisionDigest: "digest"}
	r := admission.Receipt{DecisionID: "decision", DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	if err := bindings.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	store := &admittingRegistrations{Registrations: devices.Registrations(), hook: &admissionHook{bindings: bindings}}
	legacy := server.Identity{Mode: server.ModeNoSec, Addr: "oscore:01/02"}
	if _, err := store.AddCheckedContext(server.RegisterContext{Endpoint: "device", Identity: legacy}, nil); err == nil {
		t.Fatal("legacy NoSec OSCORE identity must be rejected before admission")
	}
	now := time.Now()
	reg := &server.Registration{ID: "new-location", Endpoint: "device", Lifetime: 601 * time.Second, Identity: identity, RegisteredAt: now, LastUpdate: now}
	_, err = store.AddCheckedContext(server.RegisterContext{Endpoint: "device", Identity: identity, Fingerprint: sha256.Sum256([]byte("changed lifetime"))}, reg)
	if err != nil {
		t.Fatalf("existing admission must not require policy: %v", err)
	}
	got, ok := devices.Registrations().ByEndpoint("device")
	if !ok || got.ID != reg.ID {
		t.Fatalf("registration not committed: %+v, found %v", got, ok)
	}
}
