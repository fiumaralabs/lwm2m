package apiv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestBootstrapSessionsRealServer(t *testing.T) {
	sessions := NewMemoryBootstrapSessionStore()
	h := New(Options{Keys: deviceKeys(), BootstrapSessions: sessions})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	configs := bootstrap.NewMemoryConfigStore()
	sid := uint16(1)
	if err := configs.Put("pump", &bootstrap.BootstrapConfig{Security: map[uint16]bootstrap.SecurityConfig{0: {URI: "coap://example.com", SecurityMode: bootstrap.ModeNoSec, ServerID: &sid}}, Servers: map[uint16]bootstrap.ServerConfig{0: {ShortID: 1, Lifetime: 86400, Binding: "U"}}}); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	recorder := NewBootstrapSessionRecorder(sessions, "default")
	bs := bootstrap.New(bootstrap.Config{Configs: configs, OnSession: func(r bootstrap.Result) { completed <- recorder(r) }})
	addr, err := bs.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	c := testclient.NewBootstrap(testclient.Config{Endpoint: "pump"})
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.BootstrapRequest(ctx, c.BootstrapQuery(nil))
	if err != nil || resp.Code != codes.Changed {
		t.Fatalf("%v %v", resp, err)
	}
	if _, err := c.WaitFinish(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	w := deviceRequest(h, "GET", "/v1/devices/pump/bootstrap/sessions", "")
	var page struct {
		Items []BootstrapSession `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Kind != "request" || len(page.Items[0].Steps) == 0 {
		t.Fatalf("%s %v", w.Body.String(), err)
	}
}
