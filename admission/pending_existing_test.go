package admission

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestExistingAdmissionReceiptLookup(t *testing.T) {
	ctx := context.Background()
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "sqlite"}[persistent], func(t *testing.T) {
			var store interface {
				CommitAdmitted(context.Context, Binding, Receipt, func(*sql.Tx) error) error
				CommitExisting(context.Context, string, string, string, string, string, func(*sql.Tx) error) error
				LookupExchange(context.Context, string) (ExchangeStatus, Receipt, error)
			}
			path := filepath.Join(t.TempDir(), "db")
			if persistent {
				s, err := OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				store = s
			} else {
				store = NewMemoryBindingStore()
			}
			binding := Binding{ExchangeID: "first", TenantID: "tenant", IdentityID: "device", Fingerprint: "fp", DecisionDigest: "digest"}
			receipt := Receipt{DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
			effect := func(*sql.Tx) error { return nil }
			if err := store.CommitAdmitted(ctx, binding, receipt, effect); err != nil {
				t.Fatal(err)
			}
			if err := store.CommitExisting(ctx, "second", "tenant", "device", "fp", "request", effect); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				status, got, err := store.LookupExchange(ctx, "second")
				if err != nil || status != ExchangeAccepted || got != (Receipt{Kind: "existing_admission"}) {
					t.Errorf("lookup: status=%q receipt=%+v error=%v", status, got, err)
				}
				var tenant, identity, fingerprint string
				switch s := store.(type) {
				case *MemoryBindingStore:
					b := s.bindings["second"]
					tenant, identity, fingerprint = b.TenantID, b.IdentityID, b.Fingerprint
				case *SQLiteBindingStore:
					if err := s.db.QueryRow(`SELECT tenant_id, identity_id, fingerprint FROM admission_exchange_bindings WHERE exchange_id='second'`).Scan(&tenant, &identity, &fingerprint); err != nil {
						t.Fatal(err)
					}
				}
				if tenant != "tenant" || identity != "device" || fingerprint != "fp" {
					t.Errorf("binding: %q %q %q", tenant, identity, fingerprint)
				}
				if err := store.CommitExisting(ctx, "second", "tenant", "device", "fp", "request", effect); err != nil {
					t.Errorf("same binding replay: %v", err)
				}
				if err := store.CommitExisting(ctx, "second", "tenant", "device", "other", "request", effect); !errors.Is(err, ErrBindingConflict) {
					t.Errorf("changed fingerprint: %v", err)
				}
			}
			check()
			if persistent {
				s := store.(*SQLiteBindingStore)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				store = reopened
				check()
			}
		})
	}
}

func TestLookupLegacyExistingAdmissionReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO admission_receipts (exchange_id, decision_id, decision_digest, issued_at, expires_at, committed_at, kind, reservation_id) VALUES ('legacy', '', '', '', '', 0, 'existing_admission', '')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, receipt, err := s.LookupExchange(context.Background(), "legacy")
	if err != nil || status != ExchangeAccepted || receipt != (Receipt{Kind: "existing_admission"}) {
		t.Fatalf("legacy lookup: status=%q receipt=%+v error=%v", status, receipt, err)
	}
}

func TestLookupRejectsMalformedReceiptTimestamps(t *testing.T) {
	s, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, row := range []struct{ id, kind, issued, expires string }{
		{"ordinary", "decision", "", ""},
		{"partial", "existing_admission", "", "2026-01-01T00:00:00Z"},
		{"invalid", "existing_admission", "invalid", "invalid"},
	} {
		t.Run(row.id, func(t *testing.T) {
			_, err := s.db.Exec(`INSERT INTO admission_receipts (exchange_id,decision_id,decision_digest,issued_at,expires_at,committed_at,kind,reservation_id) VALUES (?, '', '', ?, ?, 0, ?, '')`, row.id, row.issued, row.expires, row.kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.LookupExchange(context.Background(), row.id); err == nil {
				t.Fatal("malformed timestamp accepted")
			}
		})
	}
}
