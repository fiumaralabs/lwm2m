package sqlite_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceProfileConditionalSeparateConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	old := apiv1.DeviceProfile{ID: "one", Name: "old"}
	if err := a.DeviceProfiles().Create("tenant", old); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for i, store := range []apiv1.DeviceProfileStore{a.DeviceProfiles(), b.DeviceProfiles()} {
		wg.Add(1)
		go func(i int, store apiv1.DeviceProfileStore) {
			defer wg.Done()
			next := old
			if i == 0 {
				next.Name = "first"
			} else {
				next.Name = "second"
			}
			<-start
			found, matched := store.ReplaceIfMatch("tenant", old, next)
			if !found {
				t.Error("profile disappeared")
			}
			results <- matched
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for matched := range results {
		if matched {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("got %d successful updates", wins)
	}
}
