package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestJobsContract(t *testing.T) {
	storetest.Jobs(t, func() apiv1.JobStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "jobs.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Jobs()
	})
}
