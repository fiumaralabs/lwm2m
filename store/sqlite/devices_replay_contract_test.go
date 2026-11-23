package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestDeviceReplayContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) { storetest.DeviceReplay(t, &memory.Devices{}, &memory.Events{}) })
	t.Run("sqlite", func(t *testing.T) {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "devices.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		storetest.DeviceReplay(t, db, db.Events())
	})
}
