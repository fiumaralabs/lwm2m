package admission

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAdmittedIdentitySurvivesPruneAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "admission.db")
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TABLE registrations (identity TEXT);`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b := Binding{ExchangeID: "first", TenantID: "tenant", IdentityID: "device", Fingerprint: "fp", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest", ExpiresAt: now.Add(time.Hour)}
	if err = s.CommitAdmitted(ctx, b, r, func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO registrations VALUES ('device')`); return e }); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(ctx, now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM registrations`); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsAdmitted(ctx, "tenant", "device", "fp"); err != nil || !ok {
		t.Fatalf("after prune: %v %v", ok, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ok, err := s.IsAdmitted(ctx, "tenant", "device", "fp"); err != nil || !ok {
		t.Fatalf("after restart: %v %v", ok, err)
	}
	if ok, err := s.IsAdmitted(ctx, "other", "device", "fp"); err != nil || ok {
		t.Fatalf("wrong tenant: %v %v", ok, err)
	}
}

func TestCommitExistingAtomicReceipt(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.Exec(`CREATE TABLE effects (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "first", TenantID: "tenant", IdentityID: "device", Fingerprint: "fp", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	if err = s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("failure")
	err = s.CommitExisting(ctx, "second", "tenant", "device", "fp", "request-digest", func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO effects VALUES ('x')`)
		if e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("failure: %v", err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM admission_receipts WHERE exchange_id='second'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt after failure: %d %v", count, err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM effects`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("effect after failure: %d %v", count, err)
	}
	err = s.CommitExisting(ctx, "second", "tenant", "device", "fp", "request-digest", func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO effects VALUES ('x')`); return e })
	if err != nil {
		t.Fatal(err)
	}
	var kind, decision, reservation string
	err = s.db.QueryRow(`SELECT kind,decision_id,reservation_id FROM admission_receipts WHERE exchange_id='second'`).Scan(&kind, &decision, &reservation)
	if err != nil || kind != "existing_admission" || decision != "" || reservation != "" {
		t.Fatalf("receipt: %q %q %q %v", kind, decision, reservation, err)
	}

	calls := 0
	if err = s.CommitExisting(ctx, "second", "tenant", "device", "fp", "request-digest", func(*sql.Tx) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("replay: %v calls=%d", err, calls)
	}
	if err = s.CommitExisting(ctx, "first", "tenant", "device", "fp", "request-digest", func(*sql.Tx) error { calls++; return nil }); !errors.Is(err, ErrBindingConflict) || calls != 0 {
		t.Fatalf("exchange collision: %v", err)
	}
	called := false
	err = s.CommitExisting(ctx, "third", "tenant", "other", "fp", "request-digest", func(*sql.Tx) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("unknown identity: called=%v err=%v", called, err)
	}
}

func TestMemoryAdmissionAndExisting(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryBindingStore()
	b := Binding{ExchangeID: "first", TenantID: "t", IdentityID: "i", Fingerprint: "f", DecisionDigest: "d"}
	r := Receipt{DecisionDigest: "d", ExpiresAt: time.Now().Add(time.Hour)}
	boom := errors.New("failure")
	if err := s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if ok, err := s.IsAdmitted(ctx, "t", "i", "f"); err != nil || ok {
		t.Fatalf("failed admit: %v %v", ok, err)
	}
	if err := s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsAdmitted(ctx, "t", "i", "f"); err != nil || !ok {
		t.Fatalf("admitted: %v %v", ok, err)
	}
	if err := s.CommitExisting(ctx, "next", "t", "i", "f", "request-digest", func(*sql.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, ok := s.receipts["next"]; ok {
		t.Fatal("receipt after failure")
	}
	calls := 0
	effect := func(*sql.Tx) error { calls++; return nil }
	if err := s.CommitExisting(ctx, "next", "t", "i", "f", "request-digest", effect); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitExisting(ctx, "next", "t", "i", "f", "request-digest", effect); err != nil || calls != 1 {
		t.Fatalf("replay: %v calls=%d", err, calls)
	}
	if s.receipts["next"].Kind != "existing_admission" || s.receipts["next"].DecisionID != "" || s.receipts["next"].ReservationID != "" {
		t.Fatal("incorrect receipt")
	}
	if err := s.CommitExisting(ctx, "unknown", "t", "other", "f", "request-digest", effect); !errors.Is(err, ErrNotAdmitted) || calls != 1 {
		t.Fatalf("unknown: %v", err)
	}
}

func TestExistingExchangeRequestDigest(t *testing.T) {
	ctx := context.Background()
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "sqlite"}[persistent], func(t *testing.T) {
			var store interface {
				CommitAdmitted(context.Context, Binding, Receipt, func(*sql.Tx) error) error
				CommitExisting(context.Context, string, string, string, string, string, func(*sql.Tx) error) error
			}
			path := filepath.Join(t.TempDir(), "db")
			if persistent {
				s, err := OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				store = s
			} else {
				store = NewMemoryBindingStore()
			}
			b := Binding{ExchangeID: "first", TenantID: "t", IdentityID: "i", Fingerprint: "f", DecisionDigest: "d"}
			r := Receipt{DecisionDigest: "d", ExpiresAt: time.Now().Add(time.Hour)}
			if err := store.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil }); err != nil {
				t.Fatal(err)
			}
			calls := 0
			effect := func(*sql.Tx) error { calls++; return nil }
			commit := func(digest string) error { return store.CommitExisting(ctx, "next", "t", "i", "f", digest, effect) }
			if err := commit(""); err == nil || calls != 0 {
				t.Fatalf("empty digest %v calls %d", err, calls)
			}
			if err := commit("digest-a"); err != nil {
				t.Fatal(err)
			}
			if err := commit("digest-a"); err != nil || calls != 1 {
				t.Fatalf("identical replay %v calls %d", err, calls)
			}
			if err := commit("digest-b"); !errors.Is(err, ErrBindingConflict) || calls != 1 {
				t.Fatalf("changed replay %v calls %d", err, calls)
			}
			if persistent {
				original := store.(*SQLiteBindingStore)
				if err := original.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				store = reopened
				if err := commit("digest-a"); err != nil || calls != 1 {
					t.Fatalf("restart replay %v calls %d", err, calls)
				}
				if err := commit("digest-b"); !errors.Is(err, ErrBindingConflict) || calls != 1 {
					t.Fatalf("restart changed replay %v calls %d", err, calls)
				}
			}

		})
	}
}
