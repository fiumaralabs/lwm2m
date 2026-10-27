package memory

import (
	"encoding/json"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestEventAllocationScaling(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	measure := func(n int) uint64 {
		s := Events{Clock: func() time.Time { return now }}
		data := json.RawMessage(`{"x":1}`)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < n; i++ {
			if _, err := s.Append("a", "notification", "d", data); err != nil {
				t.Fatal(err)
			}
		}
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(&s)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := measure(1000), measure(4000)
	t.Logf("1000 appends: %d bytes; 4000 appends: %d bytes", small, large)
	// Linear growth has ample slack for slice growth and runtime bookkeeping;
	// rebuilding all retained history grows approximately sixteen-fold instead.
	if large > 6*small {
		t.Fatalf("append allocation grows superlinearly: %d -> %d", small, large)
	}
	s := Events{Clock: func() time.Time { return now }}
	for i := 0; i < 4000; i++ {
		s.Append("a", "notification", "d", json.RawMessage(`{"x":1}`))
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 100; i++ {
		got, reset, err := s.List("a", store.EventFilter{Limit: 1, After: "000000000000003999"})
		if err != nil || reset || len(got) != 1 {
			t.Fatalf("poll: %v %v %v", got, reset, err)
		}
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("100 one-event replay polls: %d bytes", allocated)
	if allocated > 256*1024 {
		t.Fatalf("bounded replay polls allocated %d bytes", allocated)
	}
}

func BenchmarkEventReplayPoll(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s := Events{Clock: func() time.Time { return now }}
			var last store.Event
			for i := 0; i < n; i++ {
				last, _ = s.Append("a", "notification", "d", json.RawMessage(`{"x":1}`))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.List("a", store.EventFilter{Limit: 1, After: last.Sequence})
			}
		})
	}
}
