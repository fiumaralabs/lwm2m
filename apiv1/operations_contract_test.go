package apiv1

import (
	"context"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOperationsCreateGetCancel(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: keys, Server: srv})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"]}`)
	if w.Code != 202 || w.Header().Get("Location") == "" {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var op operations.Operation
	json.Unmarshal(w.Body.Bytes(), &op)
	path := w.Header().Get("Location")
	if op.Status != operations.Queued || op.Device != "pump" {
		t.Fatal(w.Body.String())
	}
	if w = deviceRequest(h, "GET", path, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = deviceRequest(h, "DELETE", path, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = deviceRequest(h, "GET", path, ""); !json.Valid(w.Body.Bytes()) || !containsStatus(w.Body.Bytes(), operations.Cancelled) {
		t.Fatal(w.Body.String())
	}
}
func TestOperationsDeliveryAndBoundaries(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	st := memory.NewOperations()
	var executor *operations.Executor
	srv := server.New(server.Config{OnEvent: func(ev server.Event) { executor.HandleEvent(ev) }})
	defer srv.Close()
	executor = operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: keys, Server: srv, Operations: st, Executor: executor})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"execute","path":"/3/0/4"}`)
	location := w.Header().Get("Location")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	defer c.Close()
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	w = deviceRequest(h, "GET", location+"?wait=2", "")
	if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{"type":"read","paths":["/3/0/999"]}`, `{"type":"write","path":"/1/0/1","values":[{"path":"/1/0/1","type":"integer","value":100}]}`, `{"type":"execute","path":"/3/0/4"}`} {
		w = deviceRequest(h, "POST", "/v1/devices/pump/operations?wait=2", body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"completedAt"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	other := deviceKeys()
	e.tenant = "other"
	other.entries["test"] = e
	hOther := New(Options{Keys: other, Server: srv, Operations: st, Executor: executor})
	for _, method := range []string{"GET", "DELETE"} {
		if w := deviceRequest(hOther, method, location, ""); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := deviceRequest(hOther, "POST", "/v1/devices/pump/operations", `{}`); w.Code != 404 || !strings.Contains(w.Body.String(), "device_not_found") {
		t.Fatal(w.Code, w.Body.String())
	}
	hRead := New(Options{Keys: deviceKeys(), Server: srv, Operations: st, Executor: executor})
	for _, tc := range []struct{ method, path string }{{"POST", "/v1/devices/pump/operations"}, {"DELETE", location}} {
		if w := deviceRequest(hRead, tc.method, tc.path, `{}`); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
func containsStatus(b []byte, s operations.Status) bool {
	var o operations.Operation
	return json.Unmarshal(b, &o) == nil && o.Status == s
}

func TestOperationsWaitAndReplay(t *testing.T) {
	keys := deviceKeys()
	e := keys.entries["test"]
	e.scopes += "operations:write,"
	keys.entries["test"] = e
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: keys, Server: srv, Operations: memory.NewOperations()})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	for _, tc := range []struct {
		query, body string
		code        int
	}{
		{"?wait=0", `{"type":"read","paths":["/3/0"],"ttlSeconds":0}`, 200},
		{"?wait=0", `{"type":"read","paths":["/3/0"]}`, 202},
		{"?wait=61", `{"type":"read","paths":["/3/0"]}`, 400},
		{"", `{"type":"read","paths":["/3/0"],"whenOffline":"reject"}`, 409},
	} {
		if w := deviceRequest(h, "POST", "/v1/devices/pump/operations"+tc.query, tc.body); w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	replay := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/devices/pump/operations", strings.NewReader(`{"type":"read","paths":["/3/0"]}`))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "same")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := replay()
	deviceRequest(h, "DELETE", first.Header().Get("Location"), "")
	second := replay()
	if first.Code != 202 || second.Code != 202 || first.Body.String() != second.Body.String() || first.Header().Get("Location") != second.Header().Get("Location") {
		t.Fatal(first, second)
	}
}
