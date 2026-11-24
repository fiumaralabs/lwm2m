package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestDeliveryRetainsSentEnvelope(t *testing.T) {
	events := &memory.Events{}
	hooks, deliveries := memory.NewWebhookDeliveryStores(&memory.Webhooks{})
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
	}))
	defer server.Close()
	_, err := hooks.Create("tenant", store.Webhook{ID: "hook", URL: server.URL, Status: "active", Types: []string{"*"}, Secret: "whsec_a2V5"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append("tenant", "device.updated", "device", json.RawMessage(`{"reading":9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	d := New("tenant", events, hooks, &memory.Devices{}, Options{Deliveries: deliveries})
	defer d.Close()
	if err := d.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent := <-bodies
	items, _, err := deliveries.List("tenant", "hook", "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v %v", items, err)
	}
	got, err := deliveries.Get("tenant", "hook", items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Envelope, sent) {
		t.Fatalf("stored envelope %q != sent %q", got.Envelope, sent)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	// The stored bytes are intentionally excluded from the public JSON projection.
	if bytes.Contains(encoded, sent) {
		t.Fatal("list representation exposes envelope")
	}
}
