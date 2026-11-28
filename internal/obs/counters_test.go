package obs_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/internal/obs"
)

func TestCounterMetadata(t *testing.T) {
	h := obs.New(nil, nil, true, &obs.Counters{})
	text := probe(h, http.MethodGet, "/metrics").Body.String()
	for _, line := range []string{"# TYPE lwm2m_operations_total counter", "# TYPE lwm2m_webhook_deliveries_total counter"} {
		if !strings.Contains(text, line) {
			t.Errorf("missing %q in %q", line, text)
		}
	}
}

func TestCounterLabelsAndValues(t *testing.T) {
	var c obs.Counters
	for _, status := range []string{"succeeded", "failed", "timeout", "expired", "dropped", "cancelled"} {
		c.Operation("read", status)
	}
	c.Operation("read", "succeeded")
	c.Operation("private-device", "failed")
	c.Operation("read", "queued")
	text := probe(obs.New(nil, nil, true, &c), http.MethodGet, "/metrics").Body.String()
	for _, status := range []string{"failed", "timeout", "expired", "dropped", "cancelled"} {
		line := `lwm2m_operations_total{type="read",status="` + status + `"} 1` + "\n"
		if !strings.Contains(text, line) {
			t.Errorf("missing %q in %q", line, text)
		}
	}
	if !strings.Contains(text, `lwm2m_operations_total{type="read",status="succeeded"} 2`+"\n") {
		t.Errorf("wrong succeeded value: %s", text)
	}
	if strings.Contains(text, "private-device") || strings.Contains(text, `status="queued"`) {
		t.Errorf("unbounded or nonterminal label: %s", text)
	}
}
