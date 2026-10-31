package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"testing"
)

func TestBootstrapSessionsContract(t *testing.T) {
	storetest.BootstrapSessions(t, func() apiv1.BootstrapSessionStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "bootstrap.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.BootstrapSessions()
	})
}
