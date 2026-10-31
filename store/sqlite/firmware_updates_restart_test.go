package sqlite_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestFirmwareUpdatesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updates.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := d.FirmwareUpdates()
	u := apiv1.FirmwareUpdate{ID: "fwu_1", Device: "d", Firmware: "fw"}
	if err = s.Create("tenant", u); err != nil {
		t.Fatal(err)
	}
	if !s.UpdateObserved("tenant", u.ID, "downloading", apiv1.FirmwareUpdateObservation{Method: "pull", Progress: &apiv1.FirmwareUpdateProgress{BytesSent: 2, TotalBytes: 5}}) {
		t.Fatal("observe")
	}
	want, ok := s.Get("tenant", u.ID)
	if !ok {
		t.Fatal("missing")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, ok := d.FirmwareUpdates().Get("tenant", u.ID)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if len(d.FirmwareUpdates().ListForDevice("tenant", "d")) != 1 {
		t.Fatal("list after reopen")
	}
	u.ID = "fwu_2"
	if err = d.FirmwareUpdates().Create("tenant", u); err != apiv1.ErrUpdateInProgress {
		t.Fatalf("active conflict: %v", err)
	}
}
