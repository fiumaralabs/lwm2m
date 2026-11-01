package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestDeviceCreateContract(t *testing.T) {
	storetest.DeviceCreates(t, func(t *testing.T) (store.DeviceCreateStore, store.Devices) {
		s, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s, s
	})
}
