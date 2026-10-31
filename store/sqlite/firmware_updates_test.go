package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestFirmwareUpdatesContract(t *testing.T) {
	storetest.FirmwareUpdates(t, func() apiv1.FirmwareUpdateStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "updates.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.FirmwareUpdates()
	})
}
