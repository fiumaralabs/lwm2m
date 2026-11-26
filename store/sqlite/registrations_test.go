package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
)

func registrationConformance(t *testing.T, s server.Store) {
	t.Helper()
	a := &server.Registration{ID: "one", Endpoint: "device", Lifetime: time.Hour, Objects: []link.Object{{ID: 3, Instances: []uint16{0}}}}
	if s.Add(a) != nil {
		t.Fatal("unexpected replacement")
	}
	if r, ok := s.ByEndpoint("device"); !ok || r.ID != "one" || !r.HasInstance(3, 0) {
		t.Fatal("endpoint lookup", r, ok)
	}
	b := *a
	b.ID = "two"
	if old := s.Add(&b); old == nil || old.ID != "one" {
		t.Fatal("replacement", old)
	}
	if _, ok := s.ByID("one"); ok {
		t.Fatal("old ID remains")
	}
	b.Binding = "UQ"
	if !s.Update(&b) {
		t.Fatal("update failed")
	}
	if r, ok := s.ByID("two"); !ok || r.Binding != "UQ" {
		t.Fatal("update not saved", r)
	}
	if len(s.All()) != 1 {
		t.Fatal("all")
	}
	if _, ok := s.Remove("two"); !ok {
		t.Fatal("remove")
	}
	if _, ok := s.ByEndpoint("device"); ok {
		t.Fatal("endpoint remains")
	}
	if s.Update(&b) {
		t.Fatal("updated missing record")
	}
}

func TestRegistrationConformance(t *testing.T) {
	t.Run("memory", func(t *testing.T) { registrationConformance(t, server.NewMemoryStore()) })
	t.Run("sqlite", func(t *testing.T) {
		d, err := Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		registrationConformance(t, d.Registrations())
	})
}

func TestRegistrationRestartAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &server.Registration{ID: "saved", Endpoint: "device", Lifetime: time.Hour, Objects: []link.Object{{ID: 3, Instances: []uint16{0}}}}
	d.Registrations().Add(r)
	bindings, err := admission.NewSQLiteBindingStoreDB(d.db)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected failure")
	binding := admission.Binding{ExchangeID: "x", DecisionDigest: "digest"}
	receipt := admission.Receipt{DecisionID: "d", DecisionDigest: "digest", IssuedAt: time.Unix(100, 0), ExpiresAt: time.Unix(200, 0)}
	err = bindings.CommitAdmitted(context.Background(), binding, receipt, func(tx *sql.Tx) error {
		if _, err := d.Registrations().AddTx(context.Background(), tx, &server.Registration{ID: "rolled", Endpoint: "device"}); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected failure: %v", err)
	}
	for _, table := range []string{"admission_exchange_bindings", "admission_receipts"} {
		var n int
		if err := d.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback %s: %d %v", table, n, err)
		}
	}
	err = bindings.CommitAdmitted(context.Background(), binding, receipt, func(tx *sql.Tx) error {
		_, err := d.Registrations().AddTx(context.Background(), tx, &server.Registration{ID: "committed", Endpoint: "committed"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Registrations().ByID("committed"); !ok {
		t.Fatal("committed registration missing")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, ok := d.Registrations().ByID("saved")
	if !ok || !got.HasInstance(3, 0) || got.Peer() != nil {
		t.Fatal("reload", got)
	}
	if _, ok = d.Registrations().ByID("rolled"); ok {
		t.Fatal("rollback leaked")
	}
}

func TestRegistrationAddTxCacheReconciliation(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := d.Registrations()
	original := &server.Registration{ID: "original", Endpoint: "device"}
	s.Add(original)
	tx, err := d.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddTx(context.Background(), tx, &server.Registration{ID: "discarded", Endpoint: "device"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.ByEndpoint("device"); !ok || got != original {
		t.Fatal("rollback lost live registration", got)
	}
	for i := 0; i < 10; i++ {
		live := &server.Registration{ID: fmt.Sprintf("live-%d", i), Endpoint: "device"}
		s.Add(live)
		tx, err = d.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.AddTx(context.Background(), tx, &server.Registration{ID: fmt.Sprintf("tx-%d", i), Endpoint: "device"}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.live)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("committed replacements retained %d live entries", n)
		}
		runtime.Gosched()
	}
}
