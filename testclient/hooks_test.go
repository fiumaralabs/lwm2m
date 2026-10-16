package testclient_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestSetNodesNotifiesOnceAndCallsHook(t *testing.T) {
	var mu sync.Mutex
	notes := 0
	models := server.NewModels(model.Default())
	srv := server.New(server.Config{Schema: models.Schema, Validator: models, OnEvent: func(e server.Event) {
		if _, ok := e.(server.Notification); ok {
			mu.Lock()
			notes++
			mu.Unlock()
		}
	}})
	cb := coap.New(srv)
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cb.Close(); srv.Close() }()

	c := testclient.New(testclient.Config{Endpoint: "hooks", Lifetime: 60, NoHistory: true})
	c.Set(lwm2m.MustParsePath("/3/0/9"), lwm2m.Integer(80))
	c.Set(lwm2m.MustParsePath("/3/0/10"), lwm2m.Integer(100))
	hooked := make(chan testclient.Notification, 8)
	c.SetNotifyHook(func(n testclient.Notification) { hooked <- n })
	if err := c.Dial(a.String()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.Observe(ctx, "hooks", lwm2m.MustParsePath("/3/0"), server.ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	c.SetNodes([]lwm2m.Node{
		lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/9"), lwm2m.Integer(79)),
		lwm2m.ValueNode(lwm2m.MustParsePath("/3/0/10"), lwm2m.Integer(99)),
	})
	select {
	case n := <-hooked:
		if len(n.Paths) != 1 || n.Paths[0].String() != "/3/0" || !n.Confirmable || n.Err != nil || n.Reset {
			t.Errorf("hook got %+v", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no notification hook call")
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got := notes
	mu.Unlock()
	if got != 1 || len(hooked) != 0 {
		t.Errorf("notifications = %d (hook extra %d), want 1", got, len(hooked))
	}
	if v, _ := c.Get(lwm2m.MustParsePath("/3/0/9")); v.Int != 79 {
		t.Errorf("/3/0/9 = %v", v)
	}
	if len(c.Requests()) != 0 {
		t.Errorf("NoHistory recorded %d requests", len(c.Requests()))
	}
}
