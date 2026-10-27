package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/fiumaralabs/lwm2m/webhooks"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestDaemonWebhookDelivery(t *testing.T) {
	const token = "lwm_webhook_synthetic"
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_webhook sha256:%x admin", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	type delivery struct {
		body   []byte
		header http.Header
	}
	received := make(chan delivery, 8)
	var block atomic.Bool
	stopped := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		blocking := block.Load()
		received <- delivery{body, r.Header.Clone()}
		if blocking {
			<-r.Context().Done()
			close(stopped)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	srv, native, executor, dispatcher := newDaemon(server.Config{}, leshanapi.New(leshanapi.Options{}), keys)
	t.Cleanup(func() { _ = dispatcher.Close() })
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	t.Cleanup(func() { _ = executor.Close() })
	api := httptest.NewServer(native)
	t.Cleanup(api.Close)
	req, err := http.NewRequest(http.MethodPost, api.URL+"/v1/webhooks", strings.NewReader(fmt.Sprintf(`{"url":%q,"types":["lwm2m.registration.*"],"status":"active"}`, receiver.URL)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("create webhook: status=%d body=%s err=%v", res.StatusCode, body, err)
	}
	var hook struct{ ID, Secret string }
	if err := json.Unmarshal(body, &hook); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(hook.Secret, "whsec_"))
	if err != nil || len(key) == 0 {
		t.Fatalf("missing signing key: %v", err)
	}
	cb := coap.New(srv)
	t.Cleanup(func() { _ = cb.Close() })
	addr, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client := testclient.New(testclient.Config{Endpoint: "webhook-pump", Lifetime: 1})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	check := func(kind, reason string) {
		t.Helper()
		select {
		case d := <-received:
			var event store.Event
			if err := json.Unmarshal(d.body, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "lwm2m.registration."+kind || event.Subject != "webhook-pump" || event.SpecVersion != "1.0" || event.Source != "/tenants/default" || !store.ValidSequence(event.Sequence) || event.Time.IsZero() || event.DataContentType != "application/json" {
				t.Fatalf("unexpected event: %s", d.body)
			}
			var data struct {
				Registration map[string]any
				Reason       string
			}
			if err := json.Unmarshal(event.Data, &data); err != nil || data.Registration["id"] == nil || data.Reason != reason {
				t.Fatalf("unexpected data: %s err=%v", event.Data, err)
			}
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(event.ID + "." + d.header.Get("Webhook-Timestamp") + "."))
			mac.Write(d.body)
			if d.header.Get("Webhook-Id") != event.ID || d.header.Get("Content-Type") != "application/cloudevents+json" || !hmac.Equal([]byte(d.header.Get("Webhook-Signature")), []byte("v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))) {
				t.Fatal("invalid webhook signature or headers")
			}
		case <-time.After(4 * time.Second):
			t.Fatal("webhook delivery timed out")
		}
	}
	if res, err := client.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register: %+v %v", res, err)
	}
	check("created", "")
	if res, err := client.Update(ctx, nil, nil); err != nil || res.Code != codes.Changed {
		t.Fatalf("update: %+v %v", res, err)
	}
	check("updated", "")
	if res, err := client.Deregister(ctx); err != nil || res.Code != codes.Deleted {
		t.Fatalf("deregister: %+v %v", res, err)
	}
	check("deleted", "deregistered")
	if res, err := client.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register again: %+v %v", res, err)
	}
	check("created", "")
	check("deleted", "expired")
	// Close must cancel an in-flight delivery, not merely reject later polls.
	block.Store(true)
	if res, err := client.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register before shutdown: %+v %v", res, err)
	}
	check("created", "")
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("delivery worker remains active after Close")
	}
	if err := dispatcher.Dispatch(ctx); !errors.Is(err, webhooks.ErrClosed) {
		t.Fatalf("dispatch after close: %v", err)
	}
	if err := dispatcher.Start(ctx); !errors.Is(err, webhooks.ErrClosed) {
		t.Fatalf("start after close: %v", err)
	}
}
