package apiv1

import (
	"fmt"
	"sync"
	"testing"
)

func TestFirmwareUpdateConcurrentCreate(t *testing.T) {
	s := NewMemoryFirmwareUpdateStore()
	var wg sync.WaitGroup
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- s.Create("tenant", FirmwareUpdate{ID: fmt.Sprintf("fwu_%d", i), Device: "d", Firmware: "fw"})
		}(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err != ErrUpdateInProgress {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes %d", successes)
	}
}

func TestFirmwareUpdateTransitions(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{"pending", "succeeded", false}, {"pending", "downloading", true}, {"downloading", "pending", false}, {"downloading", "downloaded", true}, {"downloaded", "updating", true}, {"updating", "succeeded", true}, {"deferred", "updating", true}, {"deferred", "cancelled", true}, {"succeeded", "failed", false}, {"failed", "pending", false}, {"cancelled", "pending", false}, {"unknown", "failed", false},
	} {
		if got := validFirmwareUpdateTransition(tc.from, tc.to); got != tc.valid {
			t.Errorf("%s -> %s: %v", tc.from, tc.to, got)
		}
	}
}

func TestFirmwareUpdateOwnedSnapshots(t *testing.T) {
	s := NewMemoryFirmwareUpdateStore()
	deferSeconds := 0
	u := FirmwareUpdate{ID: "fwu_x", Device: "d", Firmware: "fw", MaxDeferSeconds: &deferSeconds, Outcome: &FirmwareUpdateOutcome{Added: []string{"/3/0"}}}
	if err := s.Create("t", u); err != nil {
		t.Fatal(err)
	}
	*u.MaxDeferSeconds = 10
	u.Outcome.Added[0] = "bad"
	v, _ := s.Get("t", u.ID)
	if *v.MaxDeferSeconds != 0 || v.Outcome.Added[0] != "/3/0" {
		t.Fatal(v)
	}
	*v.MaxDeferSeconds = 20
	v.Outcome.Added[0] = "bad"
	v, _ = s.Get("t", u.ID)
	if *v.MaxDeferSeconds != 0 || v.Outcome.Added[0] != "/3/0" {
		t.Fatal(v)
	}
}
