package storetest

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// Events runs the immutable, tenant-local history contract for each backend.
func Events(t *testing.T, factory func(*testing.T) store.Events) {
	t.Helper()
	s := factory(t)
	data := json.RawMessage(`{"x":1}`)
	a, err := s.Append("a", "device.created", "one", data)
	if err != nil {
		t.Fatal(err)
	}
	if a.SpecVersion != "1.0" || a.Source != "/tenants/a" || a.Subject != "one" || a.Type != "device.created" || a.DataContentType != "application/json" || a.Time.IsZero() || a.Time.Location() != time.UTC || a.Sequence != "000000000000000001" || !regexp.MustCompile(`^evt_[0-7][0-9A-HJKMNP-TV-Z]{25}$`).MatchString(a.ID) {
		t.Fatalf("envelope: %+v", a)
	}
	data[5] = '9'
	if string(a.Data) != `{"x":1}` {
		t.Fatal("input aliased")
	}
	a.Data[5] = '8'
	appendEvent := func(tenant, kind, device string) store.Event {
		t.Helper()
		e, err := s.Append(tenant, kind, device, nil)
		if err != nil {
			t.Fatal(err)
		}
		if e.ID == a.ID || !store.ValidSequence(e.Sequence) {
			t.Fatalf("identity: %+v", e)
		}
		return e
	}
	b := appendEvent("a", "device.updated", "two")
	c := appendEvent("a", "job.done", "one")
	d := appendEvent("a", "device.updated", "one")
	other := appendEvent("b", "device.created", "one")
	if other.Sequence != a.Sequence {
		t.Fatal("sequence not tenant-local")
	}
	for _, tc := range []struct {
		name, tenant string
		filter       store.EventFilter
		want         []string
	}{
		{"all", "a", store.EventFilter{Limit: 10}, []string{a.Sequence, b.Sequence, c.Sequence, d.Sequence}},
		{"after", "a", store.EventFilter{Limit: 2, After: a.Sequence}, []string{b.Sequence, c.Sequence}},
		{"bounds", "a", store.EventFilter{Limit: 10, After: a.Sequence, Before: c.Sequence}, []string{b.Sequence}},
		{"empty bounds", "a", store.EventFilter{Limit: 10, After: c.Sequence, Before: a.Sequence}, nil},
		{"wildcard", "a", store.EventFilter{Limit: 10, Type: "device.*", Device: "one"}, []string{a.Sequence, d.Sequence}},
		{"types", "a", store.EventFilter{Limit: 10, Type: "device.created,job.done"}, []string{a.Sequence, c.Sequence}},
		{"literal", "a", store.EventFilter{Limit: 10, Type: "device"}, nil},
		{"history page one", "a", store.EventFilter{Limit: 2, Newest: true, Device: "one"}, []string{d.Sequence, c.Sequence}},
		{"history page two", "a", store.EventFilter{Limit: 2, Newest: true, Device: "one", Before: c.Sequence}, []string{a.Sequence}},
		{"other tenant", "b", store.EventFilter{Limit: 10}, []string{other.Sequence}},
		{"other tenant cursor", "b", store.EventFilter{Limit: 10, After: a.Sequence}, nil},
		{"missing tenant", "missing", store.EventFilter{Limit: 10}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reset, err := s.List(tc.tenant, tc.filter)
			var sequences []string
			for _, e := range got {
				sequences = append(sequences, e.Sequence)
			}
			if err != nil || reset || !reflect.DeepEqual(sequences, tc.want) {
				t.Fatalf("got %v reset=%v err=%v; want %v", sequences, reset, err, tc.want)
			}
		})
	}
	got, _, err := s.List("a", store.EventFilter{Limit: 1})
	if err != nil || len(got) != 1 || string(got[0].Data) != `{"x":1}` {
		t.Fatalf("append result aliased: %+v %v", got, err)
	}
	got[0].Data[5] = '7'
	got, _, err = s.List("a", store.EventFilter{Limit: 1})
	if err != nil || len(got) != 1 || string(got[0].Data) != `{"x":1}` {
		t.Fatal("list result aliased")
	}
	for _, payload := range []json.RawMessage{nil, []byte(`null`), []byte("{ \"x\" : 1 }"), []byte(`invalid`)} {
		e, err := s.Append("raw", "test", "one", payload)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := s.List("raw", store.EventFilter{Limit: 1, Newest: true})
		if err != nil || len(got) != 1 || !reflect.DeepEqual(e, got[0]) {
			t.Fatalf("raw payload round trip: %+v %v", got, err)
		}
	}
	for _, f := range []store.EventFilter{{}, {Limit: -1}, {Limit: 1, After: "1"}, {Limit: 1, Before: "00000000000000000x"}} {
		if _, _, err := s.List("a", f); err != store.ErrEventQuery {
			t.Fatalf("invalid filter %+v: %v", f, err)
		}
	}
}
