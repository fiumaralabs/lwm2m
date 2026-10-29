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
