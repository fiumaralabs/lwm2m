package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestEventExpiryWithClockRollback(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	s := Events{Clock: func() time.Time { return now }, Retention: time.Hour}
	var events []store.Event
	for _, minute := range []int{0, 30, 10, 40, 20, 50} {
		now = base.Add(time.Duration(minute) * time.Minute)
		e, err := s.Append("a", "notification", "d", json.RawMessage(`{"x":1}`))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	for _, tc := range []struct {
		minute    int
		remaining []int
		low       int
	}{
		{75, []int{1, 3, 4, 5}, 2}, // Expired holes, below the compaction threshold.
		{85, []int{1, 3, 5}, 4},    // Compacts; sequence and expiry orders differ.
		{101, []int{5}, 4},
		{111, nil, 5},
	} {
		now = base.Add(time.Duration(tc.minute) * time.Minute)
		for _, newest := range []bool{false, true} {
			got, reset, err := s.List("a", store.EventFilter{Limit: 10, Newest: newest, After: events[0].Sequence})
			if err != nil || !reset || len(got) != len(tc.remaining) {
				t.Fatalf("minute %d: %v %v %v", tc.minute, got, reset, err)
			}
			for i, e := range got {
				j := i
				if newest {
					j = len(tc.remaining) - 1 - i
				}
				if e.Sequence != events[tc.remaining[j]].Sequence || string(e.Data) != `{"x":1}` {
					t.Fatalf("minute %d: unexpected event %+v", tc.minute, e)
				}
			}
		}
		if s.tenants["a"].low != events[tc.low].Sequence {
			t.Fatal("watermark decreased during out-of-order expiry")
		}
		for _, e := range s.tenants["a"].items {
			if e.SpecVersion == "" && (e.Data != nil || e.ID != "") {
				t.Fatal("expired event retained payload")
			}
		}
	}
	last, err := s.Append("a", "notification", "d", nil)
	if err != nil || last.Sequence != "000000000000000007" {
		t.Fatalf("append after compaction: %+v %v", last, err)
	}
}

func BenchmarkEventRollingExpiry(b *testing.B) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := Events{Clock: func() time.Time { return now }, Retention: 4000 * time.Second}
	data := json.RawMessage(`{"x":1}`)
	for i := 0; i < 4000; i++ {
		s.Append("a", "notification", "d", data)
		now = now.Add(time.Second)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Second)
		s.Append("a", "notification", "d", data)
	}
}
