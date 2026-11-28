package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/internal/obs"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestOperationAndWebhookMetrics(t *testing.T) {
	const token = "lwm_metrics_synthetic"
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_metrics sha256:%x admin", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{`lwm2m_operations_total{type="execute",status="succeeded"}`, `lwm2m_operations_total{type="read",status="failed"}`, `lwm2m_operations_total{type="read",status="cancelled"}`, `lwm2m_operations_total{type="read",status="expired"}`, `lwm2m_webhook_deliveries_total{result="failure"}`, `lwm2m_webhook_deliveries_total{result="success"}`}
	scrape := func() map[string]uint64 {
		w := httptest.NewRecorder()
		obs.New(nil, nil, true, &obs.Process).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		values := make(map[string]uint64)
		for _, line := range strings.Split(w.Body.String(), "\n") {
			for _, label := range labels {
				if strings.HasPrefix(line, label+" ") {
					n, err := strconv.ParseUint(strings.TrimPrefix(line, label+" "), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					values[label] = n
				}
			}
		}
		if strings.Contains(w.Body.String(), "metrics-device") {
			t.Fatal("device leaked into metrics")
		}
		return values
	}
	before := scrape()
	srv, native, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys)
	defer srv.Close()
	defer executor.Close()
	defer dispatcher.Close()
	binding := coap.New(srv)
	defer binding.Close()
	addr, err := binding.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client := testclient.New(testclient.Config{Endpoint: "metrics-device"})
	defer client.Close()
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(native)
	defer api.Close()
	request := func(method, path, body string, want int) string {
		t.Helper()
		req, err := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := api.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s: %d %s %v", method, path, res.StatusCode, data, err)
		}
		return string(data)
	}
	request("POST", "/v1/devices", `{"endpoint":"metrics-device"}`, 201)
	for _, tc := range []struct{ body, status string }{
		{`{"type":"execute","path":"/3/0/4"}`, `"status":"succeeded"`},
		{`{"type":"read","paths":["/3/0/999"]}`, `"status":"failed"`},
	} {
		body := request("POST", "/v1/devices/metrics-device/operations?wait=2", tc.body, 200)
		if !strings.Contains(body, tc.status) {
			t.Fatalf("operation: %s", body)
		}
	}
	request("POST", "/v1/devices", `{"endpoint":"metrics-offline"}`, 201)
	queued := request("POST", "/v1/devices/metrics-offline/operations", `{"type":"read","paths":["/3/0/0"]}`, 202)
	var operation struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(queued), &operation); err != nil || operation.ID == "" {
		t.Fatalf("bad operation: %v", err)
	}
	request("DELETE", "/v1/operations/"+operation.ID, "", 204)
	request("DELETE", "/v1/operations/"+operation.ID, "", 409)
	expired := request("POST", "/v1/devices/metrics-offline/operations?wait=2", `{"type":"read","paths":["/3/0/0"],"ttlSeconds":0}`, 200)
	if !strings.Contains(expired, `"status":"expired"`) {
		t.Fatalf("operation: %s", expired)
	}
	var attempts atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	hook := request("POST", "/v1/webhooks", fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"]}`, receiver.URL), 201)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(hook), &created); err != nil || created.ID == "" {
		t.Fatalf("bad webhook: %v", err)
	}
	request("POST", "/v1/webhooks/"+created.ID+"/test", "", 200)
	request("POST", "/v1/webhooks/"+created.ID+"/test", "", 200)
	after := scrape()
	for _, label := range labels {
		if after[label] != before[label]+1 {
			t.Errorf("%s: before %d after %d; want exactly one increment", label, before[label], after[label])
		}
	}
}
