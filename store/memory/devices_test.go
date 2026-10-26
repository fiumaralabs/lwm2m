package memory

import (
	"github.com/fiumaralabs/lwm2m/store"
	"sync"
	"testing"
	"time"
)

func TestDevicesIsolationAndCopies(t *testing.T) {
	s := &Devices{}
	d := store.Device{Endpoint: "a", Labels: map[string]string{"x": "y"}}
	if _, err := s.Create("one", d); err != nil {
		t.Fatal(err)
	}
	d.Labels["x"] = "changed"
	got, _ := s.Get("one", "a")
	if got.Labels["x"] != "y" {
		t.Fatal("input alias")
	}
	got.Labels["x"] = "changed"
	got, _ = s.Get("one", "a")
	if got.Labels["x"] != "y" {
		t.Fatal("output alias")
	}
	if _, err := s.Create("one", d); err != store.ErrDeviceExists {
		t.Fatal(err)
	}
	if _, err := s.Get("two", "a"); err != store.ErrDeviceNotFound {
		t.Fatal(err)
	}
	if err := s.Delete("two", "a"); err != store.ErrDeviceNotFound {
		t.Fatal(err)
	}
	at := time.Now()
	s.Seen("one", "a", at)
	s.Seen("one", "a", at.Add(-time.Hour))
	got, _ = s.Get("one", "a")
	if !got.LastSeenAt.Equal(at) {
		t.Fatal("presence regressed")
	}
	*got.LastSeenAt = time.Time{}
	got, _ = s.Get("one", "a")
	if !got.LastSeenAt.Equal(at) {
		t.Fatal("timestamp alias")
	}
	s.Seen("two", "unknown", at)
	if _, err := s.Get("two", "unknown"); err != store.ErrDeviceNotFound {
		t.Fatal("auto-created")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { s.Seen("one", "a", at); _, _, _ = s.List("one", store.DeviceFilter{}, "", 50) })
	}
	wg.Wait()
	if err := s.Delete("one", "a"); err != nil {
		t.Fatal(err)
	}
}
