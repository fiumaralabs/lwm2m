package apiv1_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

type conditionalProfiles interface {
	ReplaceIfMatch(tenant string, expected, replacement apiv1.DeviceProfile) (found, matched bool)
}

func checkConditionalProfiles(t *testing.T, store apiv1.DeviceProfileStore) {
	t.Helper()
	cas, ok := store.(conditionalProfiles)
	if !ok {
		t.Fatal("store lacks atomic conditional replace")
	}
	original := apiv1.DeviceProfile{ID: "p", Name: "original", Observations: json.RawMessage(`[{"paths":["/3/0/1"]}]`), Attributes: json.RawMessage(`[]`), OnRegister: json.RawMessage(`[]`), Timeseries: json.RawMessage(`{"retentionDays":30}`), BootstrapTemplate: json.RawMessage(`{}`)}
	if err := store.Create("a", original); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Name = "changed"
	for _, alter := range []func(*apiv1.DeviceProfile){
		func(p *apiv1.DeviceProfile) { p.Name = "stale" },
		func(p *apiv1.DeviceProfile) { p.Observations = nil },
		func(p *apiv1.DeviceProfile) { p.Attributes = nil },
		func(p *apiv1.DeviceProfile) { p.OnRegister = nil },
		func(p *apiv1.DeviceProfile) { p.Timeseries = nil },
		func(p *apiv1.DeviceProfile) { p.BootstrapTemplate = nil },
	} {
		old := original
		alter(&old)
		if found, matched := cas.ReplaceIfMatch("a", old, changed); !found || matched {
			t.Fatal("stale field accepted")
		}
	}
	invalid := changed
	invalid.ID = "other"
	if found, matched := cas.ReplaceIfMatch("a", original, invalid); !found || matched {
		t.Fatal("identity change accepted")
	}

	if found, matched := cas.ReplaceIfMatch("other", original, changed); found || matched {
		t.Fatal("foreign tenant")
	}
	var wg sync.WaitGroup
	type result struct{ found, matched bool }
	results := make(chan result, 2)
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			p := original
			p.Name = name
			found, matched := cas.ReplaceIfMatch("a", original, p)
			results <- result{found, matched}
		}(name)
	}
	wg.Wait()
	close(results)
	wins, stale := 0, 0
	for r := range results {
		if !r.found {
			t.Fatal("missing profile")
		}
		if r.matched {
			wins++
		} else {
			stale++
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("wins=%d stale=%d", wins, stale)
	}
	got, ok := store.Get("a", "p")
	if !ok || (got.Name != "first" && got.Name != "second") {
		t.Fatalf("got %+v", got)
	}
	if found, matched := cas.ReplaceIfMatch("a", original, changed); !found || matched {
		t.Fatal("stale")
	}
	after, _ := store.Get("a", "p")
	if after.Name != got.Name {
		t.Fatal("stale changed profile")
	}
}

func TestDeviceProfileConditionalMemory(t *testing.T) {
	checkConditionalProfiles(t, apiv1.NewMemoryDeviceProfileStore())
}
