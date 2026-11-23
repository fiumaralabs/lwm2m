package memory

import (
	"fmt"
	"github.com/fiumaralabs/lwm2m/store"
	"testing"
)

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
