package admission

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommitAdmittedRollbackSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admission.db")
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE effect (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	fail := errors.New("injected")
	err = s.CommitAdmitted(context.Background(), b, r, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO effect VALUES ('applied')`); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"admission_exchange_bindings", "admission_receipts", "effect"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

type commitStore interface {
	CommitAdmitted(context.Context, Binding, Receipt, func(*sql.Tx) error) error
}

func TestCommitAdmittedRetriesAndRollback(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var s commitStore
			var db *SQLiteBindingStore
			if kind == "memory" {
				s = NewMemoryBindingStore()
			} else {
				var err error
				db, err = OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.db.Exec(`CREATE TABLE effects (value TEXT)`); err != nil {
					t.Fatal(err)
				}
				s = db
			}
			b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
			r := Receipt{DecisionID: "decision", DecisionDigest: "digest", IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
			boom := errors.New("failed")
			for _, panicEffect := range []bool{false, true} {
				func() {
					defer func() {
						if v := recover(); panicEffect && v == nil {
							t.Error("expected panic")
						} else if !panicEffect && v != nil {
							t.Errorf("unexpected panic: %v", v)
						}
					}()
					err := s.CommitAdmitted(context.Background(), b, r, func(tx *sql.Tx) error {
						if tx != nil {
							if _, err := tx.Exec(`INSERT INTO effects VALUES ('rolled back')`); err != nil {
								return err
							}
						}
						if panicEffect {
							panic("injected")
						}
						return boom
					})
					if !panicEffect && !errors.Is(err, boom) {
						t.Errorf("effect error %v", err)
					}
				}()
			}
			var calls atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := s.CommitAdmitted(context.Background(), b, r, func(tx *sql.Tx) error {
						calls.Add(1)
						if tx != nil {
							_, err := tx.Exec(`INSERT INTO effects VALUES ('committed')`)
							return err
						}
						return nil
					})
					if err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatalf("effect calls=%d", calls.Load())
			}
			changed := b
			changed.IdentityID = "conflict"
			if err := s.CommitAdmitted(context.Background(), changed, r, func(*sql.Tx) error { t.Error("conflicting effect called"); return nil }); !errors.Is(err, ErrBindingConflict) {
				t.Fatalf("conflict %v", err)
			}
			altered := r
			altered.DecisionID = "other"
			if err := s.CommitAdmitted(context.Background(), b, altered, func(*sql.Tx) error { t.Error("conflicting receipt called"); return nil }); !errors.Is(err, ErrBindingConflict) {
				t.Fatalf("receipt conflict %v", err)
			}
			if db != nil {
				var count int
				if err := db.db.QueryRow(`SELECT count(*) FROM effects`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("effects=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestBindingRetentionAndBorrowedDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, d := range []time.Duration{0, -time.Nanosecond} {
		if _, err = NewSQLiteBindingStoreDB(db, d); err == nil {
			t.Fatal("non-positive retention accepted")
		}
	}
	s, err := NewSQLiteBindingStoreDB(db, MaxExchangeIDLifetime)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	if err = s.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var stamp int64
	if err = db.QueryRow(`SELECT committed_at FROM admission_receipts`).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(context.Background(), r.ExpiresAt.Add(MaxExchangeIDLifetime)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM admission_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("boundary count %d err %v", count, err)
	}
	if err = s.Prune(context.Background(), r.ExpiresAt.Add(MaxExchangeIDLifetime+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"admission_exchange_bindings"} {
		if err = db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Ping(); err != nil {
		t.Fatalf("borrowed db closed: %v", err)
	}
}

func TestCommitFailureBeforeEffectSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TABLE effects (value TEXT); CREATE TRIGGER reject_receipt BEFORE INSERT ON admission_receipts BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
	r := Receipt{DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	called := false
	if err = s.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { called = true; return nil }); err == nil || called {
		t.Fatalf("failure=%v called=%v", err, called)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"admission_exchange_bindings", "admission_receipts", "effects"} {
		var n int
		if err = s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s n=%d err=%v", table, n, err)
		}
	}
}

func TestCommitAdmittedConcurrentIndependentSQLiteHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	first, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err = first.db.Exec(`CREATE TABLE effects (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
	r := Receipt{DecisionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s := first
			if i%2 == 0 {
				s = second
			}
			if err := s.CommitAdmitted(context.Background(), b, r, func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO effects VALUES ('one')`); return e }); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	var n int
	if err = first.db.QueryRow(`SELECT count(*) FROM effects`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// Pruning must never permit the same accepted exchange to run its effect twice.
func TestPruneKeepsRetryBarrier(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := NewSQLiteBindingStoreDB(db, MaxExchangeIDLifetime)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "retry", DecisionDigest: "digest"}
	now := time.Now().UTC()
	r := Receipt{DecisionDigest: "digest", IssuedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	calls := 0
	effect := func(*sql.Tx) error { calls++; return nil }
	if err := s.CommitAdmitted(context.Background(), b, r, effect); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(context.Background(), now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitAdmitted(context.Background(), b, r, effect); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(context.Background(), now.Add(31*24*time.Hour+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitAdmitted(context.Background(), b, r, effect); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("post-prune replay: %v", err)
	}
	if calls != 1 {
		t.Fatalf("effect executed %d times after prune", calls)
	}
	conflict := b
	conflict.IdentityID = "different"
	if err := s.CommitAdmitted(context.Background(), conflict, r, effect); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("conflicting retry: %v", err)
	}
}

func TestExpiredReceiptNeverApplies(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var s commitStore = NewMemoryBindingStore()
			if kind == "sqlite" {
				db, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				s = db
			}
			b := Binding{ExchangeID: "expired", DecisionDigest: "digest"}
			r := Receipt{DecisionDigest: "digest", ExpiresAt: time.Now().Add(-time.Second)}
			called := false
			err := s.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("expired receipt: err=%v called=%v", err, called)
			}
		})
	}
}

func TestPruneRetainsMinimalBarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	b := Binding{ExchangeID: "exchange", TenantID: "large-tenant", IdentityID: "device", Fingerprint: "fingerprint", RequestDigest: "request", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	calls := 0
	effect := func(*sql.Tx) error { calls++; return nil }
	if err := s.CommitAdmitted(context.Background(), b, r, effect); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitAdmitted(context.Background(), b, r, effect); err != nil || calls != 1 {
		t.Fatalf("restart retry: err=%v effects=%d", err, calls)
	}
	boundary := r.ExpiresAt.Add(DefaultBindingRetention)
	if err := s.Prune(context.Background(), boundary); err != nil {
		t.Fatal(err)
	}
	var tenant string
	if err := s.db.QueryRow(`SELECT tenant_id FROM admission_exchange_bindings WHERE exchange_id=?`, b.ExchangeID).Scan(&tenant); err != nil || tenant != b.TenantID {
		t.Fatalf("at boundary tenant=%q err=%v", tenant, err)
	}
	if err := s.Prune(context.Background(), boundary.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT tenant_id FROM admission_exchange_bindings WHERE exchange_id=?`, b.ExchangeID).Scan(&tenant); err != nil || tenant != "" {
		t.Fatalf("pruned payload tenant=%q err=%v", tenant, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := WithReceiptClock(context.Background(), func() time.Time { return boundary.Add(time.Nanosecond) })
	var expired *ReceiptExpiredError
	if err := s.CommitAdmitted(ctx, b, r, effect); !errors.As(err, &expired) {
		t.Fatalf("expired replay: %v", err)
	}
	fresh := r
	fresh.ExpiresAt = now.Add(2 * time.Hour)
	if err := s.CommitAdmitted(context.Background(), b, fresh, effect); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("fresh replay: %v", err)
	}
	if calls != 1 {
		t.Fatalf("effects: %d", calls)
	}
}

func TestReceiptExpiryBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := WithReceiptClock(context.Background(), func() time.Time { return now })
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var s commitStore = NewMemoryBindingStore()
			if kind == "sqlite" {
				db, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				s = db
			}
			for i, d := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				b := Binding{ExchangeID: string(rune('a' + i)), DecisionDigest: "d"}
				r := Receipt{DecisionDigest: "d", ExpiresAt: now.Add(d)}
				called := false
				err := s.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { called = true; return nil })
				var expired *ReceiptExpiredError
				if d <= 0 {
					if !errors.As(err, &expired) || called {
						t.Fatalf("offset %v: error=%v called=%v", d, err, called)
					}
				} else if err != nil || !called {
					t.Fatalf("offset %v: error=%v called=%v", d, err, called)
				}
			}
		})
	}
}

func TestPruneUpgradesAndPreservesLegacyBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE admission_exchange_bindings (exchange_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, identity_id TEXT NOT NULL, fingerprint TEXT NOT NULL, request_digest TEXT NOT NULL, decision_digest TEXT NOT NULL, applied INTEGER NOT NULL CHECK(applied=1)); INSERT INTO admission_exchange_bindings VALUES ('legacy','tenant','device','fp','req','digest',1)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Prune(context.Background(), time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "legacy", TenantID: "tenant", IdentityID: "device", Fingerprint: "fp", RequestDigest: "req", DecisionDigest: "digest"}
	already, err := s.BindAndMarkApplied(context.Background(), b)
	if err != nil || !already {
		t.Fatalf("legacy binding: %v %v", already, err)
	}
	var expiry sql.NullString
	if err := s.db.QueryRow(`SELECT expires_at FROM admission_exchange_bindings WHERE exchange_id='legacy'`).Scan(&expiry); err != nil || expiry.Valid {
		t.Fatalf("legacy expiry=%v err=%v", expiry, err)
	}
}
