package apiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestWebhookAttemptNumbers(t *testing.T) {
	hooks, log := memory.NewWebhookDeliveryStores(&memory.Webhooks{})
	for _, tenant := range []string{"a", "b"} {
		if _, err := hooks.Create(tenant, store.Webhook{ID: "hook"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	appendAttempt := func(tenant, id, event string) {
		log.Append(tenant, "hook", store.WebhookDelivery{ID: id, EventID: event})
	}
	attempt := func(tenant string) int {
		t.Helper()
		items, _, err := log.List(tenant, "hook", "", 1)
		if err != nil || len(items) != 1 {
			t.Fatalf("list: %v %v", items, err)
		}
		raw, _ := json.Marshal(items[0])
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		n, ok := v["attempt"].(float64)
		if !ok {
			t.Fatalf("missing attempt: %s", raw)
		}
		return int(n)
	}
	appendAttempt("a", "first", "event")
	if got := attempt("a"); got != 1 {
		t.Fatalf("first: %d", got)
	}
	appendAttempt("b", "first", "event")
	if got := attempt("b"); got != 1 {
		t.Fatalf("other tenant: %d", got)
	}
	for i := 0; i < 1001; i++ {
		appendAttempt("a", fmt.Sprint(i), "other")
	}
	appendAttempt("a", "next", "event")
	if got := attempt("a"); got != 2 {
		t.Fatalf("after eviction: %d", got)
	}
	if err := hooks.Delete("a", "hook", store.WebhookMatch{}); err != nil {
		t.Fatal(err)
	}
	appendAttempt("a", "late", "event")
	if items, _, _ := log.List("a", "hook", "", 1); len(items) != 0 {
		t.Fatal("deleted history retained")
	}
	if _, err := hooks.Create("a", store.Webhook{ID: "hook"}, ""); err != nil {
		t.Fatal(err)
	}
	appendAttempt("a", "new", "event")
	if got := attempt("a"); got != 1 {
		t.Fatalf("recreated: %d", got)
	}
	if got := attempt("b"); got != 1 {
		t.Fatalf("other tenant changed: %d", got)
	}
}

func TestWebhookAttemptManualRetry(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer receiver.Close()
	h := keyHandler(t)
	created := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"]}`, receiver.URL))
	var hook struct {
		ID string `json:"id"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &hook) != nil {
		t.Fatal(created.Code, created.Body)
	}
	base := "/v1/webhooks/" + hook.ID
	if w := keyRequest(h, "POST", base+"/test", "lwm_root_synthetic", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	list := func() []struct {
		ID      string `json:"id"`
		EventID string `json:"eventId"`
		Attempt int    `json:"attempt"`
	} {
		t.Helper()
		w := keyRequest(h, "GET", base+"/deliveries", "lwm_root_synthetic", "")
		var page struct {
			Items []struct {
				ID      string `json:"id"`
				EventID string `json:"eventId"`
				Attempt int    `json:"attempt"`
			} `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return page.Items
	}
	first := list()
	if len(first) != 1 || first[0].Attempt != 1 {
		t.Fatalf("first: %+v", first)
	}
	if w := keyRequest(h, "POST", base+"/deliveries/"+first[0].ID+"/retry", "lwm_root_synthetic", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	got := list()
	if len(got) != 2 || got[0].EventID != first[0].EventID || got[0].Attempt != 2 || got[1].Attempt != 1 {
		t.Fatalf("retry: %+v", got)
	}
}
