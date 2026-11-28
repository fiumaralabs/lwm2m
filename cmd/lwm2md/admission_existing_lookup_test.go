package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
)

func TestAdmissionExistingReceiptLookup(t *testing.T) {
	ctx := context.Background()
	s, err := admission.OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := admission.Binding{ExchangeID: "first", TenantID: "default", IdentityID: "device", Fingerprint: "first-request", RequestDigest: "first-digest", DecisionDigest: "decision-digest"}
	r := admission.Receipt{DecisionID: "decision", DecisionDigest: "decision-digest", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitExisting(ctx, "second", "default", "device", "second-request", "second-digest", func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	status, receipt, err := s.LookupExchange(ctx, "second")
	if err != nil || status != admission.ExchangeAccepted || receipt.Kind != "existing_admission" {
		t.Fatalf("existing receipt lookup: status=%q kind=%q err=%v", status, receipt.Kind, err)
	}
}
