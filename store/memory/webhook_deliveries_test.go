package memory

import (
	"fmt"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

func TestWebhookDeliveriesBoundedAfterDeletionAndLateAttempt(t *testing.T) {
	hooks, log := NewWebhookDeliveryStores(&Webhooks{})
	for i := 0; i < 1001; i++ {
		id := fmt.Sprint(i)
		if _, err := hooks.Create("tenant", store.Webhook{ID: id}, ""); err != nil {
			t.Fatal(err)
		}
		log.Append("tenant", id, store.WebhookDelivery{ID: "first"})
		if err := hooks.Delete("tenant", id, store.WebhookMatch{}); err != nil {
			t.Fatal(err)
		}
		log.Append("tenant", id, store.WebhookDelivery{ID: "late"})
	}
	if len(log.entries) != 0 {
		t.Fatalf("retained %d orphan histories", len(log.entries))
	}
}

func TestWebhookDeliveriesConcurrentDeletionAndAttempts(t *testing.T) {
	hooks, log := NewWebhookDeliveryStores(&Webhooks{})
	for i := 0; i < 1100; i++ {
		if _, err := hooks.Create("tenant", store.Webhook{ID: fmt.Sprint(i)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 1100; i++ {
		id := fmt.Sprint(i)
		wg.Add(2)
		go func() { defer wg.Done(); log.Append("tenant", id, store.WebhookDelivery{ID: "attempt"}) }()
		go func() {
			defer wg.Done()
			if err := hooks.Delete("tenant", id, store.WebhookMatch{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(log.entries) != 0 {
		t.Fatalf("retained %d orphan histories", len(log.entries))
	}
}

func TestWebhookDeliveriesRetentionAndCursors(t *testing.T) {
	hooks, log := NewWebhookDeliveryStores(&Webhooks{})
	if _, err := hooks.Create("tenant", store.Webhook{ID: "hook"}, ""); err != nil {
		t.Fatal(err)
	}
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

func TestWebhookDeliveriesLiveHistorySurvivesOtherTenantChurn(t *testing.T) {
	hooks, log := NewWebhookDeliveryStores(&Webhooks{})
	if _, err := hooks.Create("victim", store.Webhook{ID: "hook"}, ""); err != nil {
		t.Fatal(err)
	}
	log.Append("victim", "hook", store.WebhookDelivery{ID: "victim"})
	for i := 0; i < 1001; i++ {
		if _, err := hooks.Create("other", store.Webhook{ID: fmt.Sprint(i)}, ""); err != nil {
			t.Fatal(err)
		}
		log.Append("other", fmt.Sprint(i), store.WebhookDelivery{ID: "other"})
	}
	got, _, err := log.List("victim", "hook", "", 10)
	if err != nil || len(got) != 1 || got[0].ID != "victim" {
		t.Fatalf("victim history lost: %v, %v", got, err)
	}
}

func TestWebhookDeliveriesDeletionIsolationAndFailedPrecondition(t *testing.T) {
	hooks, log := NewWebhookDeliveryStores(&Webhooks{})
	for _, tenant := range []string{"a", "b"} {
		if _, err := hooks.Create(tenant, store.Webhook{ID: "shared"}, ""); err != nil {
			t.Fatal(err)
		}
		log.Append(tenant, "shared", store.WebhookDelivery{ID: tenant})
	}
	if err := hooks.Delete("a", "shared", store.WebhookMatch{Required: true, Tags: []string{"bad"}}); err != store.ErrWebhookPrecondition {
		t.Fatalf("delete: %v", err)
	}
	a, _, _ := log.List("a", "shared", "", 10)
	if len(a) != 1 {
		t.Fatalf("failed delete lost history: %v", a)
	}
	if err := hooks.Delete("a", "shared", store.WebhookMatch{}); err != nil {
		t.Fatal(err)
	}
	log.Append("a", "shared", store.WebhookDelivery{ID: "late"})
	a, _, _ = log.List("a", "shared", "", 10)
	b, _, _ := log.List("b", "shared", "", 10)
	if len(a) != 0 || len(b) != 1 || b[0].ID != "b" {
		t.Fatalf("histories after delete: a=%v b=%v", a, b)
	}
}
