package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestDevicePresenceLifecycle(t *testing.T) {
	const token = "lwm_presence_synthetic"
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_presence sha256:%x devices:read,devices:write", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	api := leshanapi.New(leshanapi.Options{Prefix: "/api"})
	var mu sync.Mutex
	counts := map[string]int{}
	srv, native, executor := newDaemon(server.Config{QueueAwake: 20 * time.Millisecond, OnEvent: func(e server.Event) {
		api.OnEvent(e)
		mu.Lock()
		counts[fmt.Sprintf("%T", e)]++
		mu.Unlock()
	}}, api, keys)
	t.Cleanup(func() { _ = srv.Close() })
	cb := coap.New(srv)
	t.Cleanup(func() { _ = cb.Close() })
	addr, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", native)
	mux.Handle("/api/", api)
	h := httptest.NewServer(mux)
	t.Cleanup(h.Close)
	t.Cleanup(func() { _ = executor.Close() })
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, h.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		res, err := h.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s err=%v", method, path, res.StatusCode, want, b, err)
		}
		return b
	}
	eventually := func(check func() bool, detail func() string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			if check() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal(detail())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	status := func(want string) {
		t.Helper()
		var body []byte
		eventually(func() bool {
			body = request("GET", "/v1/devices/pump", "", http.StatusOK)
			var view struct {
				Status       string          `json:"status"`
				LastSeenAt   *time.Time      `json:"lastSeenAt"`
				Registration json.RawMessage `json:"registration"`
			}
			if err := json.Unmarshal(body, &view); err != nil {
				t.Fatal(err)
			}
			return view.Status == want && (want == "never-seen" || view.LastSeenAt != nil) &&
				(want != "registered" || len(view.Registration) > 0 && string(view.Registration) != "null")
		}, func() string { return fmt.Sprintf("want %s: %s", want, body) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := func(queue bool) *testclient.Client {
		t.Helper()
		c := testclient.New(testclient.Config{Endpoint: "pump", Queue: queue})
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Dial(addr.String()); err != nil {
			t.Fatal(err)
		}
		return c
	}
	register := func(c *testclient.Client) {
		t.Helper()
		if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
			t.Fatalf("register: %+v %v", res, err)
		}
	}
	callback := func(kind string, want int) {
		t.Helper()
		var got int
		eventually(func() bool {
			mu.Lock()
			defer mu.Unlock()
			got = counts[kind]
			return got == want
		}, func() string { return fmt.Sprintf("callback %s: got %d want %d", kind, got, want) })
	}
	request("POST", "/v1/devices", `{"endpoint":"pump"}`, http.StatusCreated)
	status("never-seen")
	c := client(false)
	register(c)
	status("registered")
	callback("server.Registered", 1)
	if res, err := c.Update(ctx, nil, nil); err != nil || res.Code != codes.Changed {
		t.Fatalf("update: %+v %v", res, err)
	}
	callback("server.Updated", 1)
	q := client(true)
	register(q)
	status("sleeping")
	callback("server.Registered", 2)
	// Awake is not handled by DeviceEvents: it must still reach the existing callback.
	callback("server.Awake", 1)
	if res, err := q.Deregister(ctx); err != nil || res.Code != codes.Deleted {
		t.Fatalf("deregister: %+v %v", res, err)
	}
	status("offline")
	callback("server.Deregistered", 1) // replacement is carried by Registered.Replaced
	register(c)
	status("registered")
	callback("server.Registered", 3)
	if body := request("GET", "/api/clients", "", http.StatusOK); !strings.Contains(string(body), `"endpoint":"pump"`) {
		t.Fatalf("legacy registration missing: %s", body)
	}
	request("DELETE", "/v1/devices/pump", "", http.StatusNoContent)
	if _, ok := srv.Store().ByEndpoint("pump"); ok {
		t.Fatal("registration remains after device deletion")
	}
	callback("server.Deregistered", 2)
	if body := request("GET", "/api/clients", "", http.StatusOK); strings.Contains(string(body), `"endpoint":"pump"`) {
		t.Fatalf("legacy registration remains: %s", body)
	}
	request("GET", "/v1/devices/pump", "", http.StatusNotFound)
}
