package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestEventsContract(t *testing.T) {
	storetest.Events(t, func(t *testing.T) store.Events {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "events.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Events()
	})
}

func TestEventsExpiryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expiry.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s := d.Events()
	s.Clock = func() time.Time { return now }
	first, err := s.Append("a", "test", "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("a", "test", "one", nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(7 * 24 * time.Hour)
	got, reset, err := s.List("a", store.EventFilter{Limit: 10, After: first.Sequence})
	if err != nil || reset || len(got) != 1 {
		t.Fatalf("boundary: %v %v %v", got, reset, err)
	}
	now = now.Add(time.Nanosecond)
	got, reset, err = s.List("a", store.EventFilter{Limit: 10, After: first.Sequence})
	if err != nil || !reset || len(got) != 0 {
		t.Fatalf("expired: %v %v %v", got, reset, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s = d.Events()
	s.Clock = func() time.Time { return now }
	got, reset, err = s.List("a", store.EventFilter{Limit: 10, After: first.Sequence})
	if err != nil || !reset || len(got) != 0 {
		t.Fatalf("persisted watermark: %v %v %v", got, reset, err)
	}
	next, err := s.Append("a", "test", "one", nil)
	if err != nil || next.Sequence != "000000000000000003" {
		t.Fatalf("cursor reused: %+v %v", next, err)
	}
}

func TestEventsReopenAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise upgrading an existing v2 database, not just a fresh database.
	if _, err := db.Exec(`CREATE TABLE devices (tenant TEXT, endpoint TEXT, payload TEXT, last_seen TEXT DEFAULT '', PRIMARY KEY(tenant,endpoint)); CREATE TABLE operations (tenant TEXT NOT NULL, id TEXT NOT NULL, device TEXT NOT NULL, status TEXT NOT NULL, seq INTEGER NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, id)); INSERT INTO devices VALUES('a','one','{"endpoint":"one"}',''); PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get("a", "one"); err != nil {
		t.Fatal(err)
	}
	a, err := d.Events().Append("a", "device.created", "one", []byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Events().Append("b", "job.done", "two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Events().List("a", store.EventFilter{Limit: 10}); err == nil {
		t.Fatal("events must share DB lifetime")
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for tenant, want := range map[string]store.Event{"a": a, "b": b} {
		got, reset, err := d.Events().List(tenant, store.EventFilter{Limit: 10})
		if err != nil || reset || !reflect.DeepEqual(got, []store.Event{want}) {
			t.Fatalf("reopen %s: %+v %v %v", tenant, got, reset, err)
		}
	}
	next, err := d.Events().Append("a", "device.updated", "one", nil)
	if err != nil || next.Sequence != "000000000000000002" || next.ID == a.ID {
		t.Fatalf("continued cursor: %+v %v", next, err)
	}
}
