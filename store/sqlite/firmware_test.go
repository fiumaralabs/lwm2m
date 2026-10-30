package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestFirmware(t *testing.T) {
	storetest.Firmware(t, func() apiv1.FirmwareStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "firmware.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Firmware()
	})
}

func TestFirmwareMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 14 {
		t.Fatalf("schema version = %d, err = %v; want 14", version, err)
	}
}
