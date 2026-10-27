package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMemoryDevices(t *testing.T) {
	storetest.Devices(t, func(*testing.T) store.Devices { return &memory.Devices{} })
}
func TestSQLiteDevices(t *testing.T) {
	require := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "devices.db")
	s, err := sqlite.Open(path)
	require(err)
	t.Cleanup(func() { require(s.Close()) })
	storetest.Devices(t, func(*testing.T) store.Devices { return s })
	s.Seen("a", "a", time.Now().UTC())
	before, err := s.Get("a", "a")
	require(err)
	require(s.Close())
	s, err = sqlite.Open(path)
	require(err)
	after, err := s.Get("a", "a")
	require(err)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("reopen: got %+v, want %+v", after, before)
	}
}
