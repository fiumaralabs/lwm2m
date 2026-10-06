package coap

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m"
)

// Proves: GEN-07
// Freshness: every block of a block-wise request carries the same
// Request-Tag, and separate transfers use different tags, so blocks of one
// transfer cannot be swapped into another (RFC 9175 §3). Echo freshness is
// proven with OSCORE (OSC-04).
func TestRequestTagOnBlockwise(t *testing.T) {
	h := newHarness(t)
	c := h.registered("tag")
	c.Set(p("/5/0/3"), lwm2m.Integer(0)) // firmware object instance
	if r, err := c.Update(h.ctx, nil, []byte(c.ObjectLinks())); err != nil || server.CodeString(r.Code) != "2.04" {
		t.Fatal(r, err)
	}
	body := bytes.Repeat([]byte("0123456789"), 500)
	var tags [][]byte
	for i := 0; i < 2; i++ {
		before := len(c.RawRequests())
		expect(t, "2.04")(h.srv.Write(h.ctx, "tag", p("/5/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque(body))}, server.WriteOptions{}))
		blocks := c.RawRequests()[before:]
		if len(blocks) < 5 {
			t.Fatalf("transfer %d: %d datagrams, want block-wise", i, len(blocks))
		}
		for _, b := range blocks {
			if !b.HasBlock1 || len(b.RequestTag) == 0 || !bytes.Equal(b.RequestTag, blocks[0].RequestTag) {
				t.Fatalf("transfer %d: block %+v", i, b)
			}
		}
		tags = append(tags, blocks[0].RequestTag)
	}
	if bytes.Equal(tags[0], tags[1]) {
		t.Fatal("two transfers share a Request-Tag")
	}
	before := len(c.RawRequests())
	expect(t, "2.05")(h.srv.Read(h.ctx, "tag", p("/3/0/0"), server.ReadOptions{}))
	if raw := c.RawRequests()[before:]; len(raw) != 1 || raw[0].RequestTag != nil {
		t.Fatalf("Request-Tag on a non-block-wise request: %+v", raw)
	}
}
