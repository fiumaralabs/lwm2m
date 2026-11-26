package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestRegistrationAddTxBatchBounded(t *testing.T) {
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
	defer tx.Rollback()
	before := runtime.NumGoroutine()
	for i := 0; i < 1000; i++ {
		_, err = s.AddTx(context.Background(), tx, &server.Registration{ID: fmt.Sprintf("tx-%d", i), Endpoint: "device"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if delta := runtime.NumGoroutine() - before; delta > 20 {
		t.Fatalf("batch created %d goroutines", delta)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.ByEndpoint("device"); !ok || got != original {
		t.Fatal("rollback lost live registration", got)
	}

	for i := 0; i < 100; i++ {
		s.Add(&server.Registration{ID: fmt.Sprintf("live-%d", i), Endpoint: fmt.Sprintf("device-%d", i)})
	}
	tx, err = d.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 100; i++ {
		_, err = s.AddTx(context.Background(), tx, &server.Registration{ID: fmt.Sprintf("new-%d", i), Endpoint: fmt.Sprintf("device-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.live)
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("committed batch retained %d live entries, want original only", n)
		}
		runtime.Gosched()
	}
	if got, ok := s.ByEndpoint("device"); !ok || got != original {
		t.Fatal("batch lost unrelated live registration", got)
	}
}
