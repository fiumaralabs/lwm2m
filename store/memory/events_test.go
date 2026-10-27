package memory

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestEventRetention(t *testing.T) {
	for _, retention := range []time.Duration{0, time.Hour} {
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		s := Events{Clock: func() time.Time { return now }, Retention: retention}
		age := retention
		if age == 0 {
			age = 7 * 24 * time.Hour
		}
		first, _ := s.Append("a", "x", "d", nil)
		second, _ := s.Append("a", "x", "d", nil)
		now = now.Add(age)
		got, _, _ := s.List("a", store.EventFilter{Limit: 10})
		if len(got) != 2 {
			t.Fatal("expired at retention boundary")
		}
		now = now.Add(time.Nanosecond)
		got, reset, err := s.List("a", store.EventFilter{Limit: 10, After: first.Sequence})
		if err != nil || !reset || len(got) != 0 {
			t.Fatalf("expired log: %v %v %v", got, reset, err)
		}
		for _, cursor := range []string{"", second.Sequence} {
			_, reset, _ = s.List("a", store.EventFilter{Limit: 10, After: cursor})
			if reset {
				t.Fatalf("spurious reset: %q", cursor)
			}
		}
		_, reset, _ = s.List("b", store.EventFilter{Limit: 1, After: first.Sequence})
		if reset {
			t.Fatal("watermark crossed tenants")
		}
		third, _ := s.Append("a", "x", "d", nil)
		if third.Sequence != "000000000000000003" {
			t.Fatal("sequence lost after expiry")
		}
		now = now.Add(age + time.Nanosecond)
		s.Append("a", "x", "d", nil)
		if len(s.tenants["a"].items) != 1 {
			t.Fatal("append did not trim")
		}
		_, reset, _ = s.List("a", store.EventFilter{Limit: 1, After: first.Sequence})
		if !reset {
			t.Fatal("watermark lost")
		}
	}
}

func TestEventList(t *testing.T) {
	s := &Events{}
	var contract store.Events = s
	a, _ := contract.Append("a", "device.created", "one", json.RawMessage(`{"x":1}`))
	b, _ := s.Append("a", "device.updated", "two", nil)
	c, _ := s.Append("a", "job.done", "one", nil)
	s.Append("b", "device.created", "one", nil)
	for _, tc := range []struct {
		filter store.EventFilter
		want   string
	}{
		{store.EventFilter{Limit: 10}, a.Sequence + b.Sequence + c.Sequence},
		{store.EventFilter{Limit: 1, Newest: true}, c.Sequence},
		{store.EventFilter{Limit: 10, Type: "device.*", Device: "two"}, b.Sequence},
		{store.EventFilter{Limit: 10, Type: "device.created,job.done"}, a.Sequence + c.Sequence},
		{store.EventFilter{Limit: 10, Type: "device"}, ""},
		{store.EventFilter{Limit: 10, After: a.Sequence, Before: c.Sequence}, b.Sequence},
	} {
		got, reset, err := s.List("a", tc.filter)
		seq := ""
		for _, e := range got {
			seq += e.Sequence
		}
		if err != nil || reset || seq != tc.want {
			t.Fatalf("filter %+v: %s %v %v", tc.filter, seq, reset, err)
		}
	}
	a.Data[5] = '9'
	got, _, _ := s.List("a", store.EventFilter{Limit: 10})
	if string(got[0].Data) != `{"x":1}` {
		t.Fatal("append result aliased")
	}
	got[0].Data[5] = '8'
	got, _, _ = s.List("a", store.EventFilter{Limit: 10})
	if string(got[0].Data) != `{"x":1}` {
		t.Fatal("list result aliased")
	}
	for _, f := range []store.EventFilter{{}, {Limit: -1}, {Limit: 1, After: "1"}, {Limit: 1, Before: "00000000000000000x"}} {
		if _, _, err := s.List("a", f); err != store.ErrEventQuery {
			t.Fatalf("invalid query: %+v %v", f, err)
		}
	}
	got, reset, err := s.List("missing", store.EventFilter{Limit: 1})
	if len(got) != 0 || reset || err != nil {
		t.Fatal("missing tenant")
	}
}

func TestEventEnvelope(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("test", 3600))
	s := Events{Clock: func() time.Time { return now }}
	data := json.RawMessage(`{"x":1}`)
	e, err := s.Append("a", "device.created", "dev", data)
	if err != nil {
		t.Fatal(err)
	}
	if e.SpecVersion != "1.0" || e.Source != "/tenants/a" || e.Type != "device.created" || e.Subject != "dev" || e.DataContentType != "application/json" || !e.Time.Equal(now) || e.Time.Location() != time.UTC {
		t.Fatalf("envelope: %+v", e)
	}
	if !regexp.MustCompile(`^evt_[0-7][0-9A-HJKMNP-TV-Z]{25}$`).MatchString(e.ID) || e.Sequence != "000000000000000001" {
		t.Fatalf("identity: %+v", e)
	}
	data[5] = '2'
	if string(e.Data) != `{"x":1}` {
		t.Fatal("append input aliased")
	}
	next, err := s.Append("a", "device.updated", "dev", nil)
	if err != nil || next.ID == e.ID || next.Sequence != "000000000000000002" {
		t.Fatalf("next: %+v %v", next, err)
	}
	other, _ := s.Append("b", "device.created", "dev", nil)
	if other.Sequence != e.Sequence {
		t.Fatal("sequence not tenant-local")
	}
	for _, value := range []string{"", "1", "00000000000000000x", "0000000000000000000"} {
		if store.ValidSequence(value) {
			t.Fatalf("accepted %q", value)
		}
	}
	if !store.ValidSequence(e.Sequence) {
		t.Fatal("invalid sequence")
	}
}
