package memory

import (
	"fmt"
	"github.com/fiumaralabs/lwm2m/store"
	"testing"
)

func TestWebhookDeliveriesBoundedAfterDeletionAndLateAttempt(t *testing.T) {
	var log WebhookDeliveries
	var hooks Webhooks
	for i := 0; i < 1001; i++ {
		id := fmt.Sprint(i)
		if _, err := hooks.Create("tenant", store.Webhook{ID: id}, ""); err != nil {
			t.Fatal(err)
		}
		log.Append("tenant", id, store.WebhookDelivery{ID: "first"})
		if err := hooks.Delete("tenant", id, store.WebhookMatch{}); err != nil {
			t.Fatal(err)
		}
		// A request started before deletion can finish afterwards.
		log.Append("tenant", id, store.WebhookDelivery{ID: "late"})
	}
	if len(log.entries) > 1000 {
		t.Fatalf("retained %d webhook histories, want at most 1000", len(log.entries))
	}
	old, _, err := log.List("tenant", "0", "", 10)
	if err != nil || len(old) != 0 {
		t.Fatalf("oldest history not evicted: %v, %v", old, err)
	}
	latest, _, err := log.List("tenant", "1000", "", 10)
	if err != nil || len(latest) != 2 || latest[0].ID != "late" {
		t.Fatalf("latest history missing: %v, %v", latest, err)
	}
}

func TestWebhookDeliveriesRetentionAndCursors(t *testing.T) {
	var log WebhookDeliveries
	for i := 0; i < 1001; i++ {
		n := i
		log.Append("tenant", "hook", store.WebhookDelivery{ID: fmt.Sprint(i), Status: &n})
	}
	page, cursor, err := log.List("tenant", "hook", "", 2)
	if err != nil || len(page) != 2 || page[0].ID != "1000" || page[1].ID != "999" || cursor == "" {
		t.Fatalf("%+v %q %v", page, cursor, err)
	}
	*page[0].Status = -1
	again, _, _ := log.List("tenant", "hook", "", 1)
	if *again[0].Status != 1000 {
		t.Fatal("returned status aliases store")
	}
	if _, _, err := log.List("other", "hook", cursor, 2); err == nil {
		t.Fatal("cursor accepted for another tenant")
	}
	if _, _, err := log.List("tenant", "other", cursor, 2); err == nil {
		t.Fatal("cursor accepted for another webhook")
	}
	count := 2
	for cursor != "" {
		var p []store.WebhookDelivery
		p, cursor, err = log.List("tenant", "hook", cursor, 77)
		if err != nil {
			t.Fatal(err)
		}
		count += len(p)
		if len(p) > 0 && p[len(p)-1].ID == "0" {
			t.Fatal("oldest entry not evicted")
		}
	}
	if count != 1000 {
		t.Fatalf("got %d", count)
	}
}
