package apiv1

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestTimeSeriesPaginationLateInserts(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	appendPoint := func(path string, sec int, value int) {
		t.Helper()
		if err := s.Append("default", "pump", path, at.Add(time.Duration(sec)*time.Second), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []int{2, 3, 4} {
		appendPoint("/3/0/1", v, v)
	}
	appendPoint("/3/0/2", 2, 20)
	appendPoint("/3/0/2", 2, 21)
	appendPoint("/3/0/2", 3, 30)
	h := New(Options{Keys: deviceKeys(), TimeSeries: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	q := "path=/3/0/1&path=/3/0/2&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&limit=2"
	var got []float64
	cursor := ""
	for pageNo := 0; pageNo < 5; pageNo++ {
		w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+q+cursor, "")
		var page struct {
			Series []struct{ Points []TimeSeriesPoint }
			Next   string `json:"nextCursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
			t.Fatalf("page %d: %d %s %v", pageNo, w.Code, w.Body.String(), err)
		}
		for _, series := range page.Series {
			for _, p := range series.Points {
				got = append(got, p.V.(float64))
			}
		}
		if pageNo == 0 {
			appendPoint("/3/0/1", 1, 1)
		}
		if pageNo == 1 {
			appendPoint("/3/0/1", 0, 0)
			appendPoint("/3/0/2", 1, 10)
			appendPoint("/3/0/2", 2, 22)
		}
		if page.Next == "" {
			break
		}
		cursor = "&cursor=" + url.QueryEscape(page.Next)
	}
	if want := []float64{2, 3, 4, 20, 21, 22, 30}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
