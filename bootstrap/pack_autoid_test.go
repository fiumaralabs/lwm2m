package bootstrap

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: BS-12, BS-14, BS-15
// With AutoIDForSecurityObject a Pack-Request without acc is refused with
// 4.05: the BS cannot tell which /0 instance is the client's BS account, and
// a Pack instance on that ID would replace it (Anjay 3.15 sends no acc and
// keeps its BS account at /0/1). With acc the Pack is served and renumbered
// around the account; the refused client bootstraps with Bootstrap-Request.
// Found by interop/peers (Anjay).
func TestPackAutoIDNeedsAcc(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coaps://dm.example.com:5684", "id", "0123456789abcdef")
	cfg.Security = map[uint16]SecurityConfig{1: cfg.Security[0]} // collides with the BS account at /0/1
	cfg.AutoIDForSecurityObject = true
	h.put("ep", cfg)
	c := h.client("ep", testclient.Config{})

	pr, err := c.PackRequest(h.ctx, []string{"ep=ep"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Code != codes.MethodNotAllowed {
		t.Fatalf("Pack without acc: %v, want 4.05", pr.Code)
	}
	if r := h.result(); !errors.Is(r.Err, ErrPackRefused) {
		t.Fatalf("result %v", r.Err)
	}

	pr, err = c.PackRequest(h.ctx, []string{"ep=ep", "acc=</0/1>"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Code != codes.Content {
		t.Fatalf("Pack with acc: %v", pr.Code)
	}
	for _, n := range pr.Nodes {
		if n.Path.Object() == 0 && n.Path.Instance() == 1 {
			t.Fatalf("Pack writes the BS account instance: %v", n)
		}
	}
	<-h.results

	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("fallback Bootstrap-Request: %v %v", fin, res.Err)
	}
}
