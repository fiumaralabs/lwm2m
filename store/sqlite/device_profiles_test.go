package sqlite_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestDeviceProfiles(t *testing.T) {
	storetest.DeviceProfiles(t, func() apiv1.DeviceProfileStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "profiles.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.DeviceProfiles()
	})
}

func TestDeviceProfilesMigrationFrom13(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")
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
	if _, err = db.Exec(`DROP TABLE job_tasks; DROP TABLE jobs; DROP TABLE device_profiles; INSERT INTO firmware(tenant,id,payload) VALUES('t','kept','{}'); ALTER TABLE security DROP COLUMN updated_at; PRAGMA user_version=13`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, ok := d.Firmware().Get("t", "kept"); !ok {
		t.Fatal("firmware lost during migration")
	}
	if err := d.DeviceProfiles().Create("t", apiv1.DeviceProfile{ID: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.DeviceProfiles().Get("t", "new"); !ok {
		t.Fatal("profile missing after migration")
	}
}

func TestDeviceProfilesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := apiv1.DeviceProfile{ID: "one", Name: "profile", Observations: json.RawMessage(`[{"path":"/3/0/1"}]`)}
	if err := d.DeviceProfiles().Create("a", want); err != nil {
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
	if got, ok := d.DeviceProfiles().Get("a", "one"); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen: %#v %v", got, ok)
	}
}
