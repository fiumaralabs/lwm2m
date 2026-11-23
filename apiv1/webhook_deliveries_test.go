package apiv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/webhooks"
)

func TestWebhookDeliveriesIntegration(t *testing.T) {
	var status atomic.Int32
	status.Store(204)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer receiver.Close()
	keys := Keys{entries: map[string]credential{
		"root":   {hash: sha256.Sum256([]byte("lwm_root_synthetic")), tenant: "default", scopes: ",admin,"},
		"other":  {hash: sha256.Sum256([]byte("lwm_other_synthetic")), tenant: "other", scopes: ",admin,"},
		"reader": {hash: sha256.Sum256([]byte("lwm_reader_synthetic")), tenant: "default", scopes: ",devices:read,"},
	}}
	hooks, events, log := &memory.Webhooks{}, &memory.Events{}, &memory.WebhookDeliveries{}
	h := New(Options{Keys: keys, Webhooks: hooks, WebhookDeliveries: log})
	created := keyRequest(h, "POST", "/v1/webhooks", "lwm_root_synthetic", fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"]}`, receiver.URL))
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	var hook store.Webhook
	if err := json.Unmarshal(created.Body.Bytes(), &hook); err != nil {
		t.Fatal(err)
	}
	base := "/v1/webhooks/" + hook.ID
	d := webhooks.New("default", events, hooks, &memory.Devices{}, webhooks.Options{Deliveries: log})
	defer d.Close()
	first, _ := events.Append("default", "lwm2m.registration.created", "one", nil)
	if err := d.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	status.Store(503)
	second, _ := events.Append("default", "lwm2m.registration.created", "two", nil)
	if err := d.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	test := keyRequest(h, "POST", base+"/test", "lwm_root_synthetic", "")
	if test.Code != 200 {
		t.Fatal(test.Code, test.Body)
	}
	var testResult struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal(test.Body.Bytes(), &testResult); err != nil {
		t.Fatal(err)
	}
	var got []store.WebhookDelivery
	path := base + "/deliveries?limit=1"
	for path != "" {
		w := keyRequest(h, "GET", path, "lwm_root_synthetic", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var page struct {
			Items []store.WebhookDelivery `json:"items"`
			Next  string                  `json:"nextCursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("page %s", w.Body)
		}
		got = append(got, page.Items...)
		path = ""
		if page.Next != "" {
			path = base + "/deliveries?limit=1&cursor=" + url.QueryEscape(page.Next)
		}
		if len(got) > 3 {
			t.Fatal("pagination loop")
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for i, expected := range []struct {
		id, kind string
		status   int
		failed   bool
	}{{testResult.EventID, "lwm2m.webhook.test", 503, true}, {second.ID, second.Type, 503, true}, {first.ID, first.Type, 204, false}} {
		v := got[i]
		if v.ID == "" || v.EventID != expected.id || v.EventType != expected.kind || v.AttemptedAt.IsZero() || v.Status == nil || *v.Status != expected.status || v.LatencyMS <= 0 || (v.Error != "") != expected.failed {
			t.Errorf("item %d: %+v", i, v)
		}
	}
	for _, tc := range []struct {
		path, token string
		want        int
	}{{base + "/deliveries", "lwm_other_synthetic", 404}, {"/v1/webhooks/missing/deliveries", "lwm_root_synthetic", 404}, {base + "/deliveries", "lwm_reader_synthetic", 403}, {base + "/deliveries?cursor=bad", "lwm_root_synthetic", 400}} {
		w := keyRequest(h, "GET", tc.path, tc.token, "")
		if w.Code != tc.want {
			t.Errorf("%s %d %s", tc.token, w.Code, w.Body)
		}
	}
	receiver.Close()
	failed := keyRequest(h, "POST", base+"/test", "lwm_root_synthetic", "")
	if failed.Code != 200 {
		t.Fatal(failed.Code, failed.Body)
	}
	newest := keyRequest(h, "GET", base+"/deliveries?limit=1", "lwm_root_synthetic", "")
	var network struct {
		Items []store.WebhookDelivery `json:"items"`
	}
	if err := json.Unmarshal(newest.Body.Bytes(), &network); err != nil {
		t.Fatal(err)
	}
	if len(network.Items) != 1 || network.Items[0].Status != nil || network.Items[0].Error == "" {
		t.Fatalf("network failure: %s", newest.Body)
	}

}
