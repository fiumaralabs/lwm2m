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
			r := Receipt{DecisionID: "decision", DecisionDigest: "digest", IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC()}
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
	if _, err = NewSQLiteBindingStoreDB(db, MaxExchangeIDLifetime-time.Nanosecond); err == nil {
		t.Fatal("short retention accepted")
	}
	s, err := NewSQLiteBindingStoreDB(db, MaxExchangeIDLifetime)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{ExchangeID: "id", DecisionDigest: "digest"}
	r := Receipt{DecisionID: "decision", DecisionDigest: "digest"}
	if err = s.CommitAdmitted(context.Background(), b, r, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var stamp int64
	if err = db.QueryRow(`SELECT committed_at FROM admission_receipts`).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(context.Background(), time.Unix(0, stamp).Add(MaxExchangeIDLifetime)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM admission_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("boundary count %d err %v", count, err)
	}
	if err = s.Prune(context.Background(), time.Unix(0, stamp).Add(MaxExchangeIDLifetime+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"admission_exchange_bindings", "admission_receipts"} {
		if err = db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
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
	r := Receipt{DecisionDigest: "digest"}
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
	r := Receipt{DecisionDigest: "digest"}
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
