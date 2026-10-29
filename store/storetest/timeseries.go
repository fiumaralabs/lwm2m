package storetest

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// TimeSeries exercises the shared tenant-scoped history contract.
func TimeSeries(t *testing.T, fresh func() apiv1.TimeSeriesStore) {
	t.Helper()
	t.Run("OrderingIsolationAndBounds", func(t *testing.T) {
		s := fresh()
		at := time.Unix(-1, 999999999).UTC()
		for _, p := range []struct {
			tenant, endpoint, path string
			t                      time.Time
			v                      any
		}{
			{"a", "pump", "/03/0/1", at.Add(time.Nanosecond), int64(3)},
			{"a", "pump", "/3/0/1", at, int64(1)},
			{"a", "pump", "/3/0/1", at, int64(2)},
			{"b", "pump", "/3/0/1", at, int64(99)},
			{"a", "other", "/3/0/1", at, int64(99)},
			{"a", "pump", "/3/0/2", at, int64(99)},
		} {
			if err := s.Append(p.tenant, p.endpoint, p.path, p.t, p.v); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Query("a", "pump", "/3/0/1", at, at.Add(time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
		want := []apiv1.TimeSeriesPoint{{T: at, V: int64(1)}, {T: at, V: int64(2)}, {T: at.Add(time.Nanosecond), V: int64(3)}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
		got, err = s.Query("a", "pump", "/3/0/1", at, at)
		if err != nil || len(got) != 2 {
			t.Fatalf("inclusive bound: %#v %v", got, err)
		}
	})
	t.Run("ValueTypes", func(t *testing.T) {
		s := fresh()
		at := time.Unix(0, 0).UTC()
		values := []any{true, false, int(-1), int8(-8), int16(-16), int32(-32), int64(math.MinInt64), uint(1), uint8(8), uint16(16), uint32(32), uint64(math.MaxUint64), float32(1.25), float64(-2.5)}
		for _, v := range values {
			if err := s.Append("a", "e", "/1/0/1", at, v); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Query("a", "e", "/1/0/1", at, at)
		if err != nil || len(got) != len(values) {
			t.Fatalf("values: %#v %v", got, err)
		}
		for i, v := range values {
			if got[i].V != v {
				t.Errorf("value %d: %#v (%T), want %#v (%T)", i, got[i].V, got[i].V, v, v)
			}
		}
	})
	t.Run("Validation", func(t *testing.T) {
		s := fresh()
		at := time.Now()
		for _, v := range []any{nil, "text", math.NaN(), math.Inf(1), float32(math.Inf(-1))} {
			if err := s.Append("a", "e", "/1/0/1", at, v); err == nil {
				t.Errorf("accepted %v", v)
			}
		}
		for _, p := range []string{"bad", "/1/0", "/1/0/1/2/3"} {
			if err := s.Append("a", "e", p, at, 1); err == nil {
				t.Errorf("accepted path %q", p)
			}
			if _, err := s.Query("a", "e", p, at, at); err == nil {
				t.Errorf("queried path %q", p)
			}
		}
	})
}
