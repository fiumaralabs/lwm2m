package apiv1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTimeSeriesDisabled(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	w := deviceRequest(h, http.MethodGet, "/v1/devices/pump/timeseries?path=/3/0/1", "")
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), `"code":"timeseries_disabled"`) {
		t.Fatalf("disabled: %d %s", w.Code, w.Body.String())
	}
}

func TestTimeSeriesCapabilityDisabled(t *testing.T) {
	h := New(Options{Keys: deviceKeys(), Capabilities: map[string]bool{"timeseries": true, "other": true}})
	w := deviceRequest(h, http.MethodGet, "/v1/info", "")
	var info struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || w.Code != 200 || info.Capabilities["timeseries"] || !info.Capabilities["other"] {
		t.Fatalf("info: %d %s (%v)", w.Code, w.Body.String(), err)
	}
}

func TestTimeSeriesRaw(t *testing.T) {
	s := NewMemoryTimeSeriesStore()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Append("default", "pump", "/3/0/1", at, 42.0); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), TimeSeries: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w := deviceRequest(h, "GET", "/v1/devices/pump/timeseries?path=/3/0/1&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"v":42`) {
		t.Fatalf("raw: %d %s", w.Code, w.Body.String())
	}
}
