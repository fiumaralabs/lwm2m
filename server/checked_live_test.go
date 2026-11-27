package server_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type transactionalStore struct {
	*sqlite.Registrations
	db   *sql.DB
	fail atomic.Bool
}

func (s *transactionalStore) AddChecked(r *server.Registration) (*server.Registration, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	old, err := s.AddTx(context.Background(), tx, r)
	if err != nil {
		return nil, err
	}
	if s.fail.Load() {
		return nil, errors.New("injected commit failure")
	}
	return old, tx.Commit()
}
func TestCheckedSQLiteConcurrentRegisterDeregister(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registrations.db")
	devices, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devices.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := &transactionalStore{Registrations: devices.Registrations(), db: db}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	for i := 0; i < 10; i++ {
		old := h.device(testclient.Config{Endpoint: "race-peer"})
		r, err := old.Register(h.ctx)
		mustCode(t, r, err, "2.01")
		next := h.device(testclient.Config{Endpoint: "race-peer"})
		var wg sync.WaitGroup
		var registerErr error
		var registerCode codes.Code
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = old.Deregister(h.ctx) }()
		go func() {
			defer wg.Done()
			response, err := next.Register(h.ctx)
			registerErr = err
			if response != nil {
				registerCode = response.Code
			}
		}()
		wg.Wait()
		if registerErr != nil || registerCode != codes.Created {
			t.Fatalf("replacement register: %s %v", registerCode, registerErr)
		}
		current, ok := store.ByEndpoint("race-peer")
		if !ok {
			t.Fatal("replacement registration missing")
		}
		if current.ID != next.Location() {
			t.Fatalf("stale registration %q", current.ID)
		}
		expect(t, "2.05")(h.srv.Read(h.ctx, "race-peer", p("/3/0/0"), server.ReadOptions{}))
	}
}

func TestCheckedSQLitePeerAfterCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registrations.db")
	devices, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devices.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := &transactionalStore{Registrations: devices.Registrations(), db: db}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	client := h.device(testclient.Config{Endpoint: "checked-live"})
	r, err := client.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	expect(t, "2.05")(h.srv.Read(h.ctx, "checked-live", p("/3/0/0"), server.ReadOptions{}))
	store.fail.Store(true)
	oldID := client.Location()
	replacement := h.device(testclient.Config{Endpoint: "checked-live"})
	r, err = replacement.Register(h.ctx)
	mustCode(t, r, err, "4.03")
	current, ok := store.ByEndpoint("checked-live")
	if !ok || current.ID != oldID {
		t.Fatal("failed commit changed the registration")
	}
	expect(t, "2.05")(h.srv.Read(h.ctx, "checked-live", p("/3/0/0"), server.ReadOptions{}))
}
