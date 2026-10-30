package sqlite_test

import (
	"database/sql"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFirmwareReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := apiv1.Firmware{ID: "one", Name: "firmware", Version: "1.2", Description: "persisted", Labels: map[string]string{"board": "a"}, Status: "pending", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := d.Firmware().Create("a", want); err != nil {
		t.Fatal(err)
	}
	if err := d.Firmware().Create("b", apiv1.Firmware{ID: "one", Name: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got, ok := d.Firmware().Get("a", "one"); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen: %#v %v", got, ok)
	}
	if found, _ := d.Firmware().Delete("a", "one"); !found {
		t.Fatal("delete")
	}
	if got, ok := d.Firmware().Get("b", "one"); !ok || got.Name != "other" {
		t.Fatal("cross tenant deletion")
	}
}

func TestFirmwareMigrationFrom12(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE firmware; INSERT INTO devices(tenant,endpoint,payload) VALUES('a','preserved','{}'); PRAGMA user_version=12`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Get("a", "preserved"); err != nil {
		t.Fatal(err)
	}
	if err := d.Firmware().Create("a", apiv1.Firmware{ID: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Firmware().Get("a", "new"); !ok {
		t.Fatal("firmware missing after migration")
	}
}
