package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestAdmittedReregisterWhenPendingCapacityFull(t *testing.T) {
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
	identity := server.Identity{Mode: server.ModePSK, PSKIdentity: "device-key"}
	encoded, _ := json.Marshal(struct {
		Mode       server.SecurityMode
		Endpoint   string
		Credential []byte
	}{identity.Mode, "device", []byte(identity.PSKIdentity)})
	digest := sha256.Sum256(encoded)
	id := hex.EncodeToString(digest[:])
	b := admission.Binding{ExchangeID: "first", TenantID: "default", IdentityID: id, Fingerprint: "first-fingerprint", DecisionDigest: "digest"}
	r := admission.Receipt{DecisionID: "decision", DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	if err := bindings.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	store := &admittingRegistrations{Registrations: devices.Registrations(), hook: &admissionHook{bindings: bindings}, pending: make(map[[32]byte]pendingRegister)}
	now := time.Now()
	for i := 0; i < 100000; i++ {
		var key [32]byte
		binary.BigEndian.PutUint64(key[:], uint64(i))
		store.pending[key] = pendingRegister{id: "pending", at: now}
	}
	exchange := sha256.Sum256([]byte("admitted exchange"))
	reg := &server.Registration{ID: "new-location", Endpoint: "device", Lifetime: 601 * time.Second, Identity: identity, RegisteredAt: now, LastUpdate: now}
	_, err = store.AddCheckedContext(server.RegisterContext{Endpoint: "device", Identity: identity, Fingerprint: sha256.Sum256([]byte("changed lifetime")), Exchange: &exchange}, reg)
	if err != nil {
		t.Fatalf("admitted registration with full new-device capacity: %v", err)
	}
	got, ok := devices.Registrations().ByEndpoint("device")
	if !ok || got.ID != reg.ID || got.Lifetime != reg.Lifetime {
		t.Fatalf("registration not committed: %+v, found %v", got, ok)
	}
}
