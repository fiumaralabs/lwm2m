package apiv1

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTimeSeriesQueryContract(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	keys := deviceKeys()
	h := New(Options{Keys: keys, TimeSeries: s})
	for _, ep := range []string{"pump", "timeseries"} {
		if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"`+ep+`"}`); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, v := range []any{2.0, 4.0, 6.0} {
		if err := s.Append("default", "pump", "/3/0/1", at.Add(time.Duration(i)*time.Second), v); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Append("other", "pump", "/3/0/1", at, 999.0)
	_ = s.Append("default", "pump", "/3/0/2", at, true)
	query := "path=/3/0/1&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z"
	for agg, want := range map[string]string{"avg": "4", "min": "2", "max": "6", "sum": "12", "count": "3", "first": "2", "last": "6"} {
		w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+query+"&interval=300&aggregate="+agg, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"v":`+want+`}`) || strings.Contains(w.Body.String(), "999") {
			t.Errorf("%s: %d %s", agg, w.Code, w.Body.String())
		}
	}
	w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+query+"&path=/3/0/2&limit=2", "")
	var page struct {
		Series []struct {
			Path   string
			Points []TimeSeriesPoint
		}
		Next string `json:"nextCursor"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || w.Code != 200 || len(page.Series) != 2 || len(page.Series[0].Points) != 2 || page.Next == "" {
		t.Fatalf("page: %d %s %v", w.Code, w.Body.String(), e)
	}
	cursor := page.Next
	page.Next = ""
	w = deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+query+"&path=/3/0/2&limit=2&cursor="+cursor, "")
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || w.Code != 200 || len(page.Series[0].Points) != 1 || len(page.Series[1].Points) != 1 || page.Next != "" {
		t.Fatalf("page 2: %d %s %v", w.Code, w.Body.String(), e)
	}
	for _, q := range []string{"", "path=/3", "path=/3/0/1&from=bad", "path=/3/0/1&interval=0&aggregate=avg", "path=/3/0/1&aggregate=avg", "path=/3/0/1&interval=2", "path=/3/0/1&limit=501", "path=/3/0/1&cursor=bad", query + "&cursor=" + cursor} {
		w = deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+q, "")
		if w.Code != 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
			t.Errorf("bad %s: %d %s", q, w.Code, w.Body.String())
		}
	}
	w = deviceRequest(h, "GET", "/v1/devices/missing/timeseries?"+query, "")
	if w.Code != 404 {
		t.Fatalf("unknown: %d", w.Code)
	}
	w = deviceRequest(h, "GET", "/v1/info", "")
	if !strings.Contains(w.Body.String(), `"timeseries":true`) {
		t.Fatal(w.Body.String())
	}
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	if w = deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+query, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"v":2`) || !strings.Contains(w.Body.String(), `"v":999`) {
		t.Fatal(w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/devices/pump/timeseries?"+query+"&path=/3/0/2&cursor="+url.QueryEscape(cursor), "")
	if w.Code != 400 {
		t.Fatal("foreign cursor", w.Code)
	}
}

func TestTimeSeriesStoreValidationAndDefaults(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	now := time.Now().UTC()
	if err := s.Append("default", "pump", "/3/0/1", now, "text"); err == nil {
		t.Fatal("accepted text")
	}
	_ = s.Append("default", "pump", "/3/0/1", now.Add(-25*time.Hour), 10)
	_ = s.Append("default", "pump", "/3/0/1", now.Add(-time.Hour), 20)
	h := New(Options{Keys: deviceKeys(), TimeSeries: s})
	_ = deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/3/0/1", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"v":10`) || !strings.Contains(w.Body.String(), `"v":20`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
