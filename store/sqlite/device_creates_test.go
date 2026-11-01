package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestDeviceCreateFaultRollbackAndFenceAfterReopen(t *testing.T) {
	for _, point := range []string{"after-device", "before-commit"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected failure")
			s.deviceCreateHook = func(at string) error {
				if at == point {
					return fault
				}
				return nil
			}
			_, created, err := s.CreateDeviceWithOutcome(ctx, "t", "id", "hash", store.Device{Endpoint: "dev"})
			if created || !errors.Is(err, fault) {
				t.Fatalf("created=%v err=%v", created, err)
			}
			if _, err = s.Get("t", "dev"); !errors.Is(err, store.ErrDeviceNotFound) {
				t.Fatalf("device: %v", err)
			}
			var count int
			if err = s.db.QueryRow(`SELECT count(*) FROM device_create_outcomes`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("outcomes=%d err=%v", count, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			o, err := s.GetOrFenceDeviceCreate(ctx, "t", "id")
			if err != nil || o.Reason != "fenced" {
				t.Fatalf("fence=%+v err=%v", o, err)
			}
			again, created, err := s.CreateDeviceWithOutcome(ctx, "t", "id", "hash", store.Device{Endpoint: "dev"})
			if err != nil || created || !reflect.DeepEqual(o, again) {
				t.Fatalf("retry=%+v created=%v err=%v", again, created, err)
			}
		})
	}
}

func TestDeviceCreateCommittedReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.CreateDeviceWithOutcome(ctx, "t", "id", "hash", store.Device{Endpoint: "dev"})
	if err != nil || !created {
		t.Fatalf("create=%v err=%v", created, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, created, err := s.CreateDeviceWithOutcome(ctx, "t", "id", "hash", store.Device{Endpoint: "other"})
	if err != nil || created || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay=%+v created=%v err=%v", replay, created, err)
	}
	devices, _, err := s.List("t", store.DeviceFilter{}, "", 500)
	if err != nil || len(devices) != 1 || devices[0].Endpoint != "dev" {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
}

func TestDeviceCreateTwoConnectionsRace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	type result struct {
		o       store.DeviceCreateOutcome
		created bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, s := range []*Devices{a, b} {
		wg.Add(1)
		go func(s *Devices) {
			defer wg.Done()
			<-start
			o, c, e := s.CreateDeviceWithOutcome(ctx, "t", "id", "hash", store.Device{Endpoint: "dev"})
			results <- result{o, c, e}
		}(s)
	}
	close(start)
	wg.Wait()
	close(results)
	var outcomes []store.DeviceCreateOutcome
	winners := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		outcomes = append(outcomes, r.o)
		if r.created {
			winners++
		}
	}
	devices, _, err := a.List("t", store.DeviceFilter{}, "", 500)
	if err != nil || winners != 1 || len(devices) != 1 || !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatalf("winners=%d devices=%+v outcomes=%+v err=%v", winners, devices, outcomes, err)
	}
}

func TestDeviceCreateMigrationExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create("t", store.Device{Endpoint: "existing"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TABLE device_create_outcomes`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Get("t", "existing"); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 23 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var name string
	if err = s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='device_create_outcomes'`).Scan(&name); errors.Is(err, sql.ErrNoRows) || err != nil {
		t.Fatalf("migration: %v", err)
	}
}
