package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// ETS 1.2-int-12 Bootstrap via Bootstrap-Pack-Request (C.13 → C.1): the
// BS answers GET /bspack?ep=&acc= with 2.05 and the Pack in the Accept
// format (SenML CBOR, SenML JSON, LwM2M CBOR; SenML CBOR by default).
// The Pack holds the Server Account but not the BS account; a config
// instance on the client's BS-account ID (from acc) moves to a free ID;
// no Bootstrap-Finish follows. SenML names are plain paths.
// Proves: BS-12, BS-14, BS-15, BS-09
func TestInt12BootstrapPack(t *testing.T) {
	for _, accept := range []*lwm2m.ContentFormat{nil, cfp(lwm2m.FormatSenMLCBOR), cfp(lwm2m.FormatSenMLJSON), cfp(lwm2m.FormatLwM2MCBOR)} {
		h := newHarness(t)
		cfg := c1("coaps://s.example.com", "ep", "key")
		cfg.Security[1] = SecurityConfig{URI: "coaps://bs.example.com", BootstrapServer: true, SecurityMode: ModeNoSec}
		cfg.ToDelete = []string{"/1"}
		h.put("ep", cfg)
		c := h.client("ep", testclient.Config{Version: "1.2"})
		c.Set(p("/1/7/0"), lwm2m.Integer(7))          // replaced: /1 is in the Pack
		c.Set(p("/5/0/1"), lwm2m.String("coap://fw")) // kept: /5 is not
		acc := c.BSAccountLinks()
		if acc != "</0/1>" {
			t.Fatalf("acc %q", acc)
		}
		pr, err := c.PackRequest(h.ctx, []string{"ep=ep", "acc=" + acc}, accept)
		if err != nil {
			t.Fatal(err)
		}
		want := lwm2m.FormatSenMLCBOR
		if accept != nil {
			want = *accept
		}
		if pr.Code != codes.Content || pr.Format == nil || *pr.Format != want {
			t.Fatalf("accept %v: %s format %v", accept, server.CodeString(pr.Code), pr.Format)
		}
		res := h.result()
		if !res.Pack || res.Err != nil || res.Endpoint != "ep" {
			t.Fatalf("result %+v", res)
		}
		for _, n := range pr.Nodes {
			if n.Path.Truncate(2) == p("/0/1") {
				t.Fatalf("Pack overwrites the BS account: %v", n)
			}
		}
		// Config /0/0 kept its ID (acc says the BS account is /0/1).
		if _, id, _, ok := c.ServerAccount(); !ok || string(id) != "ep" {
			t.Fatalf("no server account:\n%s", c.Dump())
		}
		if v, _ := c.Get(p("/0/1/1")); !v.Bool {
			t.Fatal("BS account lost")
		}
		if _, ok := c.Get(p("/1/7/0")); ok {
			t.Fatal("/1/7 survived the Pack")
		}
		if _, ok := c.Get(p("/5/0/1")); !ok {
			t.Fatal("object absent from the Pack deleted")
		}
		if want == lwm2m.FormatSenMLJSON {
			var recs []map[string]any
			_ = json.Unmarshal(pr.Body, &recs)
			for _, r := range recs {
				if n, _ := r["n"].(string); !strings.HasPrefix(n, "/0/") && !strings.HasPrefix(n, "/1/") {
					t.Fatalf("SenML name %q", n)
				}
			}
		}
		if len(c.Requests()) != 0 {
			t.Fatalf("BS sent requests after a Pack: %s", methods(c.Requests()))
		}
	}

	// acc names /0/0: the config's /0/0 moves to the first free ID.
	h := newHarness(t)
	h.put("ep", c1("coaps://s.example.com", "ep", "key"))
	c := h.client("ep", testclient.Config{Version: "1.2"})
	pr, err := c.PackRequest(h.ctx, []string{"ep=ep", "acc=</0/0>,</21/0>"}, nil)
	if err != nil || pr.Code != codes.Content {
		t.Fatalf("%v %v", pr, err)
	}
	for _, n := range pr.Nodes {
		if n.Path.Truncate(2) == p("/0/0") {
			t.Fatalf("Pack writes over acc instance /0/0: %v", n)
		}
	}
	h.result()
}

// Bootstrap-Pack-Request errors (C §6.1.7.7, T Tbl 6.4.2-1): 5.01 when the
// BS does not implement it, 4.05 when it refuses (by config, no Server
// Account, or deletes a Pack cannot express), 4.06 for an Accept it cannot
// produce, 4.00 for acc links with parameters or outside the BS account
// and for an ep not bound to the identity. After a refusal the classic
// Bootstrap-Request still works.
// Proves: BS-12, BS-14, BS-15
func TestBootstrapPackErrors(t *testing.T) {
	off := newHarness(t, func(c *Config) { c.DisablePack = true })
	off.put("ep", c1("coap://s.example.com", "id", "key"))
	oc := off.client("ep", testclient.Config{})
	pr, err := oc.PackRequest(off.ctx, []string{"ep=ep"}, nil)
	if err != nil || pr.Code != codes.NotImplemented {
		t.Fatalf("disabled: %v %v", pr, err)
	}

	h := newHarness(t)
	refuse := c1("coap://s.example.com", "id", "key")
	refuse.RefusePack = true
	h.put("refuse", refuse)
	h.put("noaccount", &BootstrapConfig{Servers: map[uint16]ServerConfig{0: {ShortID: 1}}})
	del := c1("coap://s.example.com", "id", "key")
	del.ToDelete = []string{"/2"}
	h.put("delete", del)
	root := c1("coap://s.example.com", "id", "key")
	root.ToDelete = []string{"/"}
	h.put("root", root)
	partial := c1("coap://s.example.com", "id", "key")
	partial.Writes = []Write{{p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(60))}}}
	h.put("partial", partial)
	h.put("ok", c1("coap://s.example.com", "id", "key"))
	c := h.client("", testclient.Config{})
	for _, tc := range []struct {
		name   string
		query  []string
		accept *lwm2m.ContentFormat
		want   codes.Code
	}{
		{"refused by config", []string{"ep=refuse"}, nil, codes.MethodNotAllowed},
		{"no server account", []string{"ep=noaccount"}, nil, codes.MethodNotAllowed},
		{"delete outside the Pack", []string{"ep=delete"}, nil, codes.MethodNotAllowed},
		{"delete /", []string{"ep=root"}, nil, codes.MethodNotAllowed},
		{"resource write", []string{"ep=partial"}, nil, codes.MethodNotAllowed},
		{"Accept TLV", []string{"ep=ok"}, cfp(lwm2m.FormatTLV), codes.NotAcceptable},
		{"Accept text", []string{"ep=ok"}, cfp(lwm2m.FormatText), codes.NotAcceptable},
		{"acc with params", []string{"ep=ok", "acc=</0/1>;ssid=1"}, nil, codes.BadRequest},
		{"acc object path", []string{"ep=ok", "acc=</0>"}, nil, codes.BadRequest},
		{"acc not BS account", []string{"ep=ok", "acc=</3/0>"}, nil, codes.BadRequest},
		{"acc not link format", []string{"ep=ok", "acc=/0/1"}, nil, codes.BadRequest},
		{"unknown ep", []string{"ep=nobody"}, nil, codes.BadRequest},
		{"no ep", nil, nil, codes.BadRequest},
	} {
		pr, err := c.PackRequest(h.ctx, tc.query, tc.accept)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if pr.Code != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, server.CodeString(pr.Code), server.CodeString(tc.want))
		}
	}
	r, err := c.Raw(h.ctx, codes.POST, "/bspack", []string{"ep=ok"}, nil, nil)
	mustCode(t, r, err, codes.MethodNotAllowed)

	// The client falls back to Bootstrap-Request (BS-15): the BS serves it.
	fb := h.client("refuse", testclient.Config{})
	pr, _ = fb.PackRequest(h.ctx, []string{"ep=refuse"}, nil)
	if pr.Code != codes.MethodNotAllowed {
		t.Fatal(server.CodeString(pr.Code))
	}
	for len(h.results) > 0 {
		<-h.results
	}
	if fin, res := h.bootstrap(fb, fb.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("fallback: %s %v", server.CodeString(fin), res.Err)
	}
}
