package coap

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// A registered client that stays silent longer than go-coap's idle close
// keeps its DTLS session: the next downlink reaches it. Before the fix the
// connection was closed after 16 s and the request failed with EOF
// (Zephyr interop int-109 and every test after an idle period).
func TestIdleRegisteredConnStaysOpen(t *testing.T) {
	s := server.New(server.Config{RequestTimeout: 5 * time.Second})
	b := New(s)
	b.held.idle = 100 * time.Millisecond
	t.Cleanup(func() { _ = b.Close(); _ = s.Close() })
	if err := s.Security().Put(server.SecurityInfo{Endpoint: "idle", PSKIdentity: "idle", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	d, err := b.ListenDTLS("127.0.0.1:0", DTLSConfig{CIDLength: 6})
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "idle", PSKIdentity: "idle", PSKKey: []byte("0123456789abcdef"), CID: true})
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Zephyr"))
	if err := c.Dial(d.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r, err := c.Register(ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("register: %v %v", r, err)
	}
	time.Sleep(5 * time.Second) // > one go-coap inactivity tick (4 s) past idle
	resp, err := s.Read(ctx, "idle", lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
	if err != nil || resp.Code != codes.Content {
		t.Fatalf("read after idle: %v %v", resp, err)
	}
}
