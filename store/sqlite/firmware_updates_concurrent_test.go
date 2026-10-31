package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestFirmwareUpdatesConcurrentCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updates.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	other, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stores := []*sqlite.FirmwareUpdates{d.FirmwareUpdates(), other.FirmwareUpdates()}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = stores[i].Create("t", apiv1.FirmwareUpdate{ID: []string{"fwu_a", "fwu_b"}[i], Device: "d", Firmware: "fw"})
		}(i)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, apiv1.ErrUpdateInProgress) {
			conflicts++
		} else {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("results %v", errs)
	}
}

func TestFirmwareUpdatesWaitForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updates.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	store := d.FirmwareUpdates()
	if err := store.Create("t", apiv1.FirmwareUpdate{ID: "fwu_x", Device: "d", Firmware: "fw"}); err != nil {
		t.Fatal(err)
	}
	lock, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer lock.Exec("ROLLBACK")
	result := make(chan any, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				result <- p
			}
		}()
		result <- store.UpdateStatus("t", "fwu_x", "failed")
	}()
	select {
	case r := <-result:
		t.Fatalf("update finished while writer held lock: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := lock.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if r != true {
			t.Fatalf("update after lock released: %v", r)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("update did not finish")
	}
}

func TestFirmwareUpdatesConcurrentTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updates.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = a.FirmwareUpdates().Create("t", apiv1.FirmwareUpdate{ID: "fwu_x", Device: "d", Firmware: "fw"}); err != nil {
		t.Fatal(err)
	}
	stores := []*sqlite.FirmwareUpdates{a.FirmwareUpdates(), b.FirmwareUpdates()}
	results := make([]bool, 2)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = stores[i].UpdateStatus("t", "fwu_x", []string{"failed", "cancelled"}[i])
		}(i)
	}
	wg.Wait()
	if results[0] == results[1] {
		t.Fatalf("terminal race: %v", results)
	}
}
