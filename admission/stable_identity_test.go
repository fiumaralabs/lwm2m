package admission

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStableAdmittedIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var s interface {
				CommitAdmitted(context.Context, Binding, Receipt, func(*sql.Tx) error) error
				IsAdmitted(context.Context, string, string, string) (bool, error)
				CommitExisting(context.Context, string, string, string, string, string, func(*sql.Tx) error) error
			}
			if backend == "sqlite" {
				db, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "admission.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				s = db
			} else {
				s = NewMemoryBindingStore()
			}
			ctx := context.Background()
			b := Binding{ExchangeID: "first", TenantID: "tenant", IdentityID: "device", Fingerprint: "register-one", DecisionDigest: "decision"}
			r := Receipt{DecisionDigest: "decision", ExpiresAt: time.Now().Add(time.Hour)}
			if err := s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil }); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				tenant, identity, fingerprint string
				want                          bool
			}{
				{"tenant", "device", "register-two", true}, {"other", "device", "register-two", false}, {"tenant", "other", "register-two", false},
			} {
				got, err := s.IsAdmitted(ctx, tc.tenant, tc.identity, tc.fingerprint)
				if err != nil || got != tc.want {
					t.Errorf("IsAdmitted(%q,%q,%q) = %v, %v; want %v", tc.tenant, tc.identity, tc.fingerprint, got, err, tc.want)
				}
			}
			calls := 0
			effect := func(*sql.Tx) error { calls++; return nil }
			if err := s.CommitExisting(ctx, "second", "tenant", "device", "register-two", "digest", effect); err != nil {
				t.Fatal(err)
			}
			if err := s.CommitExisting(ctx, "second", "tenant", "device", "register-two", "digest", effect); err != nil || calls != 1 {
				t.Fatalf("replay: %v calls %d", err, calls)
			}
			if err := s.CommitExisting(ctx, "second", "tenant", "device", "register-three", "digest", effect); !errors.Is(err, ErrBindingConflict) || calls != 1 {
				t.Fatalf("changed fingerprint replay: %v calls %d", err, calls)
			}
		})
	}
}

func TestStableIdentityMigratesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE admission_identities (tenant_id TEXT NOT NULL, identity_id TEXT NOT NULL, fingerprint TEXT NOT NULL, PRIMARY KEY (tenant_id, identity_id, fingerprint)); INSERT INTO admission_identities VALUES ('tenant','device','old-fingerprint')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ok, err := s.IsAdmitted(context.Background(), "tenant", "device", "new-fingerprint")
	if err != nil || !ok {
		t.Fatalf("legacy admitted identity: %v %v", ok, err)
	}
}
