package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestContinuousFailureDisablesWebhook(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events, hooks := &memory.Events{}, &memory.Webhooks{}
	success := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !success {
			w.WriteHeader(503)
		}
	}))
	defer receiver.Close()
	hooks.Create("a", store.Webhook{ID: "h", URL: receiver.URL, Status: "active", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	d := New("a", events, hooks, &memory.Devices{}, Options{Now: func() time.Time { return now }})
	defer d.Close()
	poll := func() {
		t.Helper()
		if err := d.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	status := func(want string) {
		t.Helper()
		h, err := hooks.Get("a", "h")
		if err != nil || h.Status != want {
			t.Fatalf("status=%s want=%s err=%v", h.Status, want, err)
		}
	}
	events.Append("a", "x", "d", nil)
	poll()
	now = now.Add(48 * time.Hour)
	// The original delivery expired at 24h. Success on another device resets endpoint failures.
	success = true
	events.Append("a", "x", "other", nil)
	poll()
	status("active")
	now = now.Add(time.Hour)
	success = false
	events.Append("a", "x", "d", nil)
	poll()
	now = now.Add(72*time.Hour - time.Nanosecond)
	poll()
	status("active")
	now = now.Add(time.Nanosecond)
	poll()
	status("disabled")
	poll()
	found, _, err := events.List("a", store.EventFilter{Type: "lwm2m.webhook.disabled", Limit: 100})
	if err != nil || len(found) != 1 || found[0].Subject != "h" {
		t.Fatalf("disabled events=%+v err=%v", found, err)
	}
	other, _, err := events.List("b", store.EventFilter{Limit: 100})
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant events=%v err=%v", other, err)
	}
}
