package bootstrap

import (
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: SEC-06, SEC-07
// Bootstrap-Request and (1.2) Bootstrap-Pack-Request bind ep to the
// authenticated identity exactly like Register: ep2's PSK claiming ep1 is
// 4.00 on both, its own ep is served; a NoSec request naming an endpoint
// that has DTLS credentials is 4.00 on both.
func TestBootstrapEndpointBinding(t *testing.T) {
	h := newHarness(t)
	for _, si := range []server.SecurityInfo{
		{Endpoint: "ep1", PSKIdentity: "ep1-bs", PSKKey: []byte(bsKey)},
		{Endpoint: "ep2", PSKIdentity: "ep2-bs", PSKKey: []byte(bsKey)},
	} {
		if err := h.sec.Put(si); err != nil {
			t.Fatal(err)
		}
	}
	h.put("ep1", c1("coaps://s.example.com", "ep1", "k"))
	h.put("ep2", c1("coaps://s.example.com", "ep2", "k"))

	psk := h.client("ep2", testclient.Config{PSKIdentity: "ep2-bs", PSKKey: []byte(bsKey)})
	nosec := h.client("ep1", testclient.Config{})
	for _, c := range []*testclient.BootstrapClient{psk, nosec} {
		if pr, err := c.PackRequest(h.ctx, []string{"ep=ep1"}, nil); err != nil || pr.Code != codes.BadRequest {
			t.Fatalf("Pack-Request ep=ep1: %v %v", pr, err)
		}
		r, err := c.BootstrapRequest(h.ctx, []string{"ep=ep1"})
		mustCode(t, r, err, codes.BadRequest)
	}
	if pr, err := psk.PackRequest(h.ctx, []string{"ep=ep2"}, nil); err != nil || pr.Code != codes.Content {
		t.Fatalf("Pack-Request ep=ep2: %v %v", pr, err)
	}
}
