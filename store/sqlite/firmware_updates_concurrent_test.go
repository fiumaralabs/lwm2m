package sqlite_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

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
