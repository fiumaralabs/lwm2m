package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"testing"
	"time"
)

func TestTimeSeries(t *testing.T) {
	storetest.TimeSeries(t, func() apiv1.TimeSeriesStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return d.TimeSeries()
	})
}
func TestTimeSeriesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(123, 456).UTC()
	if err = d.TimeSeries().Append("a", "e", "/1/0/1", at, uint64(^uint64(0))); err != nil {
		t.Fatal(err)
	}
	if err = d.TimeSeries().Append("a", "e", "/1/0/1", at, int64(2)); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.TimeSeries().Append("a", "e", "/1/0/1", at, int64(3)); err != nil {
		t.Fatal(err)
	}
	got, err := d.TimeSeries().Query("a", "e", "/1/0/1", at, at)
	if err != nil || len(got) != 3 || !got[0].T.Equal(at) || got[0].V != uint64(^uint64(0)) || got[1].V != int64(2) || got[2].V != int64(3) {
		t.Fatalf("reopen: %#v %v", got, err)
	}
}
