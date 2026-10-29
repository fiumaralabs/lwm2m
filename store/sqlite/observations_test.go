package sqlite_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestObservations(t *testing.T) {
	storetest.Observations(t, func() store.Observations {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "observations.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Observations()
	})
}

func TestObservationsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 2, 3, 4, 5, 6, 789, time.UTC)
	v := store.Observation{Tenant: "tenant", ID: "id", Device: "device", Paths: []string{"/3/0"}, Attributes: map[string]json.RawMessage{"pmin": json.RawMessage(`20`)}, Persistent: true, Status: "active", Source: "api", Prefix: "gw", Format: "tlv", Accept: "cbor", EstablishedAt: &at, LastNotificationAt: &at, NotificationCount: 9, Error: json.RawMessage(`{"code":"x"}`)}
	s := d.Observations()
	if err := s.Create("tenant", v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDelivery("tenant", "id", func(v *store.Observation) { v.NotificationCount = 12 }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAttributes("tenant", "id", map[string]json.RawMessage{"pmax": json.RawMessage(`30`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.End("tenant", "id"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get("tenant", "id")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	after, err := d.Observations().Get("tenant", "id")
	if err != nil || !reflect.DeepEqual(before, after) || after.Status != "ended" || after.NotificationCount != 12 {
		t.Fatalf("reopen: %+v %v", after, err)
	}
	list, err := d.Observations().List("tenant", "device")
	if err != nil || len(list) != 1 || !reflect.DeepEqual(after, list[0]) {
		t.Fatalf("reopened list: %+v %v", list, err)
	}
}
