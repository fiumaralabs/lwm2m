package server

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

// Downlink Block1 uses 512-byte blocks: a 5000-byte firmware write is 10
// datagrams, not 5. With 1024-byte blocks the Zephyr interop client (1 KiB
// of RX buffers) dropped every first block (test_blockwise_1..4).
func TestBlock1Uses512ByteBlocks(t *testing.T) {
	h := newHarness(t)
	c := h.registered("blk512")
	c.Set(p("/5/0/3"), lwm2m.Integer(0))
	if r, err := c.Update(h.ctx, nil, []byte(c.ObjectLinks())); err != nil || CodeString(r.Code) != "2.04" {
		t.Fatal(r, err)
	}
	body := bytes.Repeat([]byte("1234567890"), 500)
	before := len(c.RawRequests())
	expect(t, "2.04")(h.srv.Write(h.ctx, "blk512", p("/5/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque(body))}, WriteOptions{}))
	if n := len(c.RawRequests()) - before; n != 10 {
		t.Fatalf("%d Block1 datagrams for 5000 bytes, want 10 (512-byte blocks)", n)
	}
}
