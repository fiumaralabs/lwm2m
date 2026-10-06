package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
)

// A client Block1 request whose blocks each carry a new token (Zephyr's
// Send) is reassembled by URI, not token: the Send handler gets the whole
// SenML payload. go-coap alone handed over just the last block, a 4.00
// (Zephyr interop test_blockwise_4).
func TestBlock1NewTokenPerBlock(t *testing.T) {
	h := newHarness(t)
	cc, err := udp.Dial(h.addr, options.WithBlockwise(false, blockwise.SZX1024, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reg, err := cc.Post(ctx, "/rd", message.AppLinkFormat, strings.NewReader("</3/0>"), message.Option{ID: message.URIQuery, Value: []byte("ep=tok")}, message.Option{ID: message.URIQuery, Value: []byte("lwm2m=1.1")}, message.Option{ID: message.URIQuery, Value: []byte("lt=300")})
	if err != nil || reg.Code() != codes.Created {
		t.Fatalf("register: %v %v", reg, err)
	}

	big := strings.Repeat("0123456789", 150) // 1500 bytes
	body, _ := json.Marshal([]map[string]any{{"bn": "/3/0/", "n": "0", "vs": big}})
	const szx = blockwise.SZX512
	for num := int64(0); num*szx.Size() < int64(len(body)); num++ {
		end := min((num+1)*szx.Size(), int64(len(body)))
		more := end < int64(len(body))
		blk, _ := blockwise.EncodeBlockOption(szx, num, more)
		req, err := cc.NewPostRequest(ctx, "/dp", message.AppSenmlJSON, bytes.NewReader(body[num*szx.Size():end]))
		if err != nil {
			t.Fatal(err)
		}
		tok, _ := message.GetToken()
		req.SetToken(tok) // a new token for every block
		req.SetOptionUint32(message.Block1, blk)
		resp, err := cc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		want := codes.Changed
		if more {
			want = codes.Continue
		}
		if resp.Code() != want {
			t.Fatalf("block %d: %v, want %v", num, resp.Code(), want)
		}
		if got, err := resp.GetOptionUint32(message.Block1); err != nil || got != blk {
			t.Fatalf("block %d: Block1 echo %v %v, want %v", num, got, err, blk)
		}
	}
	ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(SendReceived); return ok }).(SendReceived)
	if len(ev.Nodes) != 1 || ev.Nodes[0].Value.Str != big {
		t.Fatalf("send nodes %+v", ev.Nodes)
	}

	// A continuation without block 0 is 4.08.
	blk, _ := blockwise.EncodeBlockOption(szx, 3, false)
	req, _ := cc.NewPostRequest(ctx, "/dp", message.AppSenmlJSON, bytes.NewReader([]byte("x")))
	req.SetOptionUint32(message.Block1, blk)
	if resp, err := cc.Do(req); err != nil || resp.Code() != codes.RequestEntityIncomplete {
		t.Fatalf("orphan block: %v %v", resp, err)
	}
}
