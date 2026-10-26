package apiv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"strings"
	"testing"
	"time"
)

func deviceKeys() Keys {
	return Keys{entries: map[string]credential{"test": {hash: sha256.Sum256([]byte("lwm_test_secret")), scopes: ",devices:read,devices:write,", tenant: "default"}}}
}
func TestDevicePaginationAndTenants(t *testing.T) {
	keys := deviceKeys()
	h := New(Options{Keys: keys})
	for _, ep := range []string{"a", "b", "c"} {
		if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"`+ep+`"}`); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	var page struct {
		Items      []deviceView
		NextCursor string
	}
	w := deviceRequest(h, "GET", "/v1/devices?limit=2", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 2 || page.Items[0].Endpoint != "a" || page.NextCursor == "" {
		t.Fatal(w.Body.String())
	}
	w = deviceRequest(h, "GET", "/v1/devices?limit=2&cursor="+page.NextCursor, "")
	if !strings.Contains(w.Body.String(), `"endpoint":"c"`) || strings.Contains(w.Body.String(), "nextCursor") {
		t.Fatal(w.Body.String())
	}
	for _, query := range []string{"limit=0", "limit=501", "limit=no", "cursor=bogus", "status=invalid", "q=x&cursor=" + page.NextCursor} {
		if w := deviceRequest(h, "GET", "/v1/devices?"+query, ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	for _, query := range []string{"q=missing", "status=registered"} {
		if w := deviceRequest(h, "GET", "/v1/devices?"+query, ""); !strings.Contains(w.Body.String(), `"items":[]`) {
			t.Fatal(w.Body.String())
		}
	}
	entry := keys.entries["test"]
	entry.tenant = "other"
	keys.entries["test"] = entry
	for _, method := range []string{"GET", "DELETE"} {
		if w := deviceRequest(h, method, "/v1/devices/a", ""); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := deviceRequest(h, "GET", "/v1/devices?cursor="+page.NextCursor, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	entry.tenant = "default"
	entry.scopes = ",devices:read,"
	keys.entries["test"] = entry
	for _, method := range []string{"POST", "DELETE"} {
		if w := deviceRequest(h, method, "/v1/devices/a", `{}`); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	entry.scopes = ",devices:write,"
	keys.entries["test"] = entry
	if w := deviceRequest(h, "GET", "/v1/devices", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestDeviceRegistrationLifecycle(t *testing.T) {
	d := &memory.Devices{}
	srv := server.New(server.Config{QueueAwake: time.Millisecond, OnEvent: DeviceEvents(d, "default")})
	defer srv.Close()
	b := coap.New(srv)
	defer b.Close()
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Keys: deviceKeys(), Devices: d, Server: srv})
	status := func(want string) {
		t.Helper()
		w := deviceRequest(h, "GET", "/v1/devices/pump", "")
		var v deviceView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v.Status != want {
			t.Fatalf("want %s: %s", want, w.Body.String())
		}
		if want == "offline" && v.LastSeenAt == nil {
			t.Fatal("missing history")
		}
		if want == "registered" && v.Registration == nil {
			t.Fatal("missing registration")
		}
	}
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	status("never-seen")
	c := testclient.New(testclient.Config{Endpoint: "pump"})
	defer c.Close()
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register: %+v %v", res, err)
	}
	status("registered")
	if res, err := c.Deregister(ctx); err != nil || res.Code != codes.Deleted {
		t.Fatalf("deregister: %+v %v", res, err)
	}
	status("offline")
	q := testclient.New(testclient.Config{Endpoint: "pump", Queue: true})
	defer q.Close()
	if err := q.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	if res, err := q.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("queue register: %+v %v", res, err)
	}
	time.Sleep(10 * time.Millisecond)
	status("sleeping")
	w := deviceRequest(h, "DELETE", "/v1/devices/pump", "")
	if w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if _, ok := srv.Store().ByEndpoint("pump"); ok {
		t.Fatal("registration remains")
	}
	if w := deviceRequest(h, "GET", "/v1/devices/pump", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register again: %+v %v", res, err)
	}
	if _, err := c.Deregister(ctx); err != nil {
		t.Fatal(err)
	}
	status("offline")
	unknown := testclient.New(testclient.Config{Endpoint: "unknown"})
	defer unknown.Close()
	if err := unknown.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := unknown.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/unknown", ""); w.Code != 404 {
		t.Fatal("auto-created without settings", w.Body.String())
	}
}
