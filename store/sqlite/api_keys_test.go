package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"testing"
)

func TestAPIKeys(t *testing.T) {
	storetest.APIKeys(t, func(t *testing.T) store.APIKeys {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.APIKeys()
	})
}
