package apiv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTimeSeriesLargeAggregates(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := s.Append("default", "pump", "/3/0/1", at, 1e308); err != nil {
			t.Fatal(err)
		}
	}
	h := New(Options{Keys: deviceKeys(), TimeSeries: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	q := "/v1/devices/pump/timeseries?path=/3/0/1&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&interval=300&aggregate="
	w := deviceRequest(h, "GET", q+"avg", "")
	var body struct {
		Series []struct{ Points []TimeSeriesPoint }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || len(body.Series) != 1 || len(body.Series[0].Points) != 1 || body.Series[0].Points[0].V != 1e308 {
		t.Fatalf("avg: %d %s: %v", w.Code, w.Body.String(), err)
	}
	w = deviceRequest(h, "GET", q+"sum", "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("overflow sum: %d %s", w.Code, w.Body.String())
	}
}

func TestTimeSeriesSumCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []float64
		want   float64
	}{
		{"intermediate overflow", []float64{1e308, 1e308, -1e308}, 1e308},
		{"small residual", []float64{1e308, 1, -1e308}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryTimeSeriesStore()
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, v := range tc.values {
				if err := s.Append("default", "pump", "/3/0/1", at, v); err != nil {
					t.Fatal(err)
				}
			}
			h := New(Options{Keys: deviceKeys(), TimeSeries: s})
			if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/3/0/1&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&interval=300&aggregate=sum", "")
			var body struct {
				Series []struct{ Points []TimeSeriesPoint }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || len(body.Series) != 1 || len(body.Series[0].Points) != 1 || body.Series[0].Points[0].V != tc.want {
				t.Fatalf("sum: %d %s: %v; want %g", w.Code, w.Body.String(), err, tc.want)
			}
		})
	}
}

func TestTimeSeriesIntegerAggregatePrecision(t *testing.T) {
	cases := []struct {
		name   string
		values []any
		want   map[string]string
	}{
		{"signed cancellation", []any{int64(9007199254740993), int64(-9007199254740992)}, map[string]string{"sum": "1", "avg": "0.5", "min": "-9007199254740992", "max": "9007199254740993"}},
		{"unsigned ordering", []any{uint64(9007199254740993), uint64(9007199254740992)}, map[string]string{"sum": "18014398509481985", "min": "9007199254740992", "max": "9007199254740993"}},
		{"exact large average", []any{int64(9007199254740993), int64(9007199254740993)}, map[string]string{"avg": "9007199254740993"}},
		{"mixed integral result", []any{uint64(9007199254740993), 0.0}, map[string]string{"sum": "9007199254740993"}},
		{"mixed types", []any{uint64(9007199254740993), int64(-9007199254740992), 0.5}, map[string]string{"sum": "1.5", "avg": "0.5", "min": "-9007199254740992", "max": "9007199254740993"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryTimeSeriesStore()
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, v := range tc.values {
				if err := s.Append("default", "pump", "/3/0/1", at, v); err != nil {
					t.Fatal(err)
				}
			}
			h := New(Options{Keys: deviceKeys(), TimeSeries: s})
			if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			for agg, want := range tc.want {
				w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/3/0/1&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&interval=300&aggregate="+agg, "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"v":`+want+`}`) {
					t.Errorf("%s want %s: %d %s", agg, want, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestTimeSeriesCanonicalPaths(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Append("default", "pump", "/03/0/1", at, 42); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), TimeSeries: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/3/0/1", "/03/0/1"} {
		w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path="+path+"&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"v":42`) {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
