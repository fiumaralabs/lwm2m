package bootstrap

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func methods(rs []testclient.Request) string {
	var out []string
	for _, r := range rs {
		m := map[codes.Code]string{codes.GET: "GET", codes.PUT: "PUT", codes.POST: "POST", codes.DELETE: "DELETE"}[r.Code]
		out = append(out, m+" "+r.Path)
	}
	return strings.Join(out, ", ")
}

// ETS 1.1-int-0 Client Initiated Bootstrap (config C.13): Bootstrap-Request
// 2.04, Bootstrap-Writes of /0 and /1 answered 2.04, Bootstrap-Discover
// 2.05, Bootstrap-Finish 2.04; the BS account stays untouched.
// Proves: BS-01, BS-03, BS-07, BS-16
func TestInt0ClientInitiatedBootstrap(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coaps://server.example.com:5684", "ep0", "secret0123456789")
	cfg.Discover = true
	h.put("ep0", cfg)
	c := h.client("ep0", testclient.Config{})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s, err %v\n%s", server.CodeString(fin), res.Err, c.Dump())
	}
	if got, want := methods(c.Requests()), "GET /, PUT /0/0, PUT /1/0, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
	reqs := c.Requests()
	if a := reqs[0].Accept; a == nil || *a != lwm2m.FormatLinkFormat {
		t.Fatal("Bootstrap-Discover without Accept link-format")
	}
	for _, r := range reqs[1:3] {
		if r.Code != codes.PUT || r.Format == nil || *r.Format != lwm2m.FormatTLV {
			t.Fatalf("write %+v: want PUT in TLV (no pct)", r)
		}
	}
	if fin := reqs[3]; fin.Body != nil || fin.Format != nil || len(fin.Queries) > 0 {
		t.Fatalf("Finish must be an empty POST /bs: %+v", fin)
	}
	// Discover result: the client's BS account /0/1 has no ssid.
	if len(res.Discover) == 0 || res.Discover[0].Path != lwm2m.Root {
		t.Fatalf("discover %v", res.Discover)
	}
	// Server Account /0/0 + /1/0 paired by ssid 1; BS account kept.
	uri, id, key, ok := c.ServerAccount()
	if !ok || uri != "coaps://server.example.com:5684" || string(id) != "ep0" || string(key) != "secret0123456789" {
		t.Fatalf("server account %q %q %q\n%s", uri, id, key, c.Dump())
	}
	if v, _ := c.Get(p("/0/0/10")); v.Int != 1 {
		t.Fatal("/0/0/10 not 1")
	}
	if v, _ := c.Get(p("/1/0/0")); v.Int != 1 {
		t.Fatal("/1/0/0 not 1")
	}
	if v, _ := c.Get(p("/0/1/1")); !v.Bool {
		t.Fatal("BS account lost")
	}
	if len(res.Steps) != 4 || res.Steps[3].Code != codes.POST || res.Steps[3].Result != codes.Changed {
		t.Fatalf("steps %+v", res.Steps)
	}
}

// Bootstrap-Request error codes (C §6.1.7.1, T Tbl 6.4.2-1): unknown ep,
// missing ep with no identity to derive it from, NoSec for an endpoint
// with credentials give 4.00; an unusable pct gives 4.15.
// Proves: BS-01, BS-20
func TestBootstrapRequestErrors(t *testing.T) {
	h := newHarness(t)
	h.put("known", c1("coap://s.example.com", "x", "y"))
	h.put("secured", c1("coap://s.example.com", "x", "y"))
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "secured", PSKIdentity: "secured-id", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	c := h.client("", testclient.Config{})
	for _, tc := range []struct {
		name  string
		code  codes.Code
		path  string
		query []string
		want  codes.Code
	}{
		{"unknown ep", codes.POST, "/bs", []string{"ep=nobody"}, codes.BadRequest},
		{"no ep over NoSec", codes.POST, "/bs", nil, codes.BadRequest},
		{"NoSec for secured ep", codes.POST, "/bs", []string{"ep=secured"}, codes.BadRequest},
		{"pct not a format", codes.POST, "/bs", []string{"ep=known", "pct=abc"}, codes.BadRequest},
		{"pct text", codes.POST, "/bs", []string{"ep=known", "pct=0"}, codes.UnsupportedMediaType},
		{"pct OMA JSON", codes.POST, "/bs", []string{"ep=known", "pct=11543"}, codes.UnsupportedMediaType},
		{"GET /bs", codes.GET, "/bs", []string{"ep=known"}, codes.MethodNotAllowed},
		{"other path", codes.POST, "/rd", []string{"ep=known"}, codes.NotFound},
	} {
		r, err := c.Raw(h.ctx, tc.code, tc.path, tc.query, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Code != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, server.CodeString(r.Code), server.CodeString(tc.want))
		}
	}
	if len(c.Requests()) != 0 {
		t.Fatalf("a refused request started a session: %s", methods(c.Requests()))
	}
}

// BS-02: the Bootstrap-Writes use the client's preferred format (pct)
// when the config forces none; TLV without pct; the config overrides
// pct. Bootstrap SenML names are plain /o/i/r paths (BS-09).
// Proves: BS-02, BS-03, BS-09
func TestPreferredContentFormat(t *testing.T) {
	for _, tc := range []struct {
		pct, force *lwm2m.ContentFormat
		want       lwm2m.ContentFormat
	}{
		{nil, nil, lwm2m.FormatTLV},
		{cfp(lwm2m.FormatTLV), nil, lwm2m.FormatTLV},
		{cfp(lwm2m.FormatSenMLJSON), nil, lwm2m.FormatSenMLJSON},
		{cfp(lwm2m.FormatSenMLCBOR), nil, lwm2m.FormatSenMLCBOR},
		{cfp(lwm2m.FormatLwM2MCBOR), nil, lwm2m.FormatLwM2MCBOR},
		{cfp(lwm2m.FormatSenMLCBOR), cfp(lwm2m.FormatSenMLJSON), lwm2m.FormatSenMLJSON},
	} {
		h := newHarness(t)
		cfg := c1("coap://s.example.com", "id", "key")
		cfg.ContentFormat = tc.force
		h.put("ep", cfg)
		c := h.client("ep", testclient.Config{Version: "1.2"})
		fin, res := h.bootstrap(c, c.BootstrapQuery(tc.pct))
		if fin != codes.Changed || res.Err != nil || res.Format != tc.want {
			t.Fatalf("pct %v force %v: finish %s err %v format %d", tc.pct, tc.force, server.CodeString(fin), res.Err, res.Format)
		}
		for _, r := range c.Requests() {
			if r.Code != codes.PUT {
				continue
			}
			if *r.Format != tc.want {
				t.Fatalf("write %s in %d, want %d", r.Path, *r.Format, tc.want)
			}
			if tc.want == lwm2m.FormatSenMLJSON {
				var recs []map[string]any
				if err := json.Unmarshal(r.Body, &recs); err != nil {
					t.Fatal(err)
				}
				bn := ""
				for _, rec := range recs {
					if b, ok := rec["bn"].(string); ok {
						bn = b
					}
					name := bn + rec["n"].(string)
					if !strings.HasPrefix(name, r.Path+"/") {
						t.Fatalf("SenML name %q not under %s (BS-09)", name, r.Path)
					}
				}
			}
		}
		if _, id, _, ok := c.ServerAccount(); !ok || string(id) != "id" {
			t.Fatalf("account not provisioned in %d:\n%s", tc.want, c.Dump())
		}
	}
}

// A client that answers 4.15 to a Bootstrap-Write (T Tbl 6.7-1) gets the
// write again in another multi-value format; the format that worked is
// kept for the rest of the session.
// Proves: BS-03
func TestWriteUnsupportedFormatFallback(t *testing.T) {
	h := newHarness(t)
	h.put("ep", c1("coap://s.example.com", "id", "key"))
	c := h.client("ep", testclient.Config{Formats: []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR}})
	fin, res := h.bootstrap(c, c.BootstrapQuery(cfp(lwm2m.FormatTLV)))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	var got []string
	for _, s := range res.Steps {
		if s.Code == codes.PUT {
			got = append(got, s.Path.String()+":"+server.CodeString(s.Result)+":"+s.Format.String())
		}
	}
	tlv, cbor := lwm2m.FormatTLV.String(), lwm2m.FormatSenMLCBOR.String()
	if want := "/0/0:4.15:" + tlv + ", /0/0:2.04:" + cbor + ", /1/0:2.04:" + cbor; strings.Join(got, ", ") != want {
		t.Fatalf("writes %v, want %s", got, want)
	}
}

// An error answer to a Delete or Write does not end the session: the BS
// still ends it with Bootstrap-Finish, and the client's 4.06 (inconsistent
// configuration) is reported as a failed bootstrap.
// Proves: BS-03, BS-07
func TestWriteErrorThenFinishRejected(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coap://s.example.com", "id", "key")
	cfg.ToDelete = []string{"/5"}
	h.put("ep", cfg)
	c := h.client("ep", testclient.Config{})
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		switch {
		case r.Code == codes.PUT && r.Path == "/1/0":
			return codes.BadRequest, nil, nil, true
		case r.Code == codes.DELETE:
			return codes.BadRequest, nil, nil, true
		}
		return 0, nil, nil, false
	})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.NotAcceptable || !errors.Is(res.Err, ErrFinishRejected) {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if got, want := methods(c.Requests()), "DELETE /5, PUT /0/0, PUT /1/0, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
}

// ETS 1.1-int-4 Bootstrap Delete: Bootstrap-Delete /0 and /1 come before
// the writes; the client's own /1 instance is gone, the BS account and
// /3/0 survive; Bootstrap-Read /1 shows the new instance. Delete "/" is
// sent as is and leaves only the BS account and /3/0 (Leshan
// bootstrapDeleteAll).
// Proves: BS-04, BS-06
func TestInt4BootstrapDelete(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coap://s.example.com", "id", "key")
	cfg.Discover = true
	cfg.ToDelete = []string{"/0", "/1"}
	cfg.Read = []string{"/1"}
	h.put("ep", cfg)
	c := h.client("ep", testclient.Config{})
	c.Set(p("/1/2/0"), lwm2m.Integer(7)) // locally created /1/2 (zephyr int-4)
	c.Set(p("/1/2/1"), lwm2m.Integer(60))
	c.Set(p("/0/3/1"), lwm2m.Boolean(false))
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if got, want := methods(c.Requests()), "GET /, DELETE /0, DELETE /1, PUT /0/0, PUT /1/0, GET /1, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
	if _, ok := c.Get(p("/1/2/0")); ok {
		t.Fatal("/1/2 not deleted")
	}
	if _, ok := c.Get(p("/0/3/1")); ok {
		t.Fatal("/0/3 not deleted")
	}
	if v, _ := c.Get(p("/0/1/1")); !v.Bool {
		t.Fatal("BS account deleted")
	}
	read := res.Reads["/1"]
	if len(read) == 0 || read[0].Path.Truncate(2) != p("/1/0") {
		t.Fatalf("Bootstrap-Read /1: %v", read)
	}
	if acc := c.Requests()[5].Accept; acc == nil || *acc != lwm2m.FormatTLV {
		t.Fatal("Bootstrap-Read without Accept")
	}

	// Delete "/" (BS-04 without an Object ID).
	h2 := newHarness(t)
	cfg2 := c1("coap://s.example.com", "id", "key")
	cfg2.ToDelete = []string{"/"}
	h2.put("ep", cfg2)
	c2 := h2.client("ep", testclient.Config{})
	c2.Set(p("/5/0/1"), lwm2m.String("coap://fw"))
	if fin, res := h2.bootstrap(c2, c2.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if c2.Requests()[0].Code != codes.DELETE || c2.Requests()[0].Path != "/" {
		t.Fatalf("first request %+v", c2.Requests()[0])
	}
	if _, ok := c2.Get(p("/5/0/1")); ok {
		t.Fatal("/5/0 survived Delete /")
	}
	if _, ok := c2.Get(p("/3/0/0")); !ok {
		t.Fatal("/3/0 deleted")
	}
}

// ETS 1.1-int-8 Bootstrap Read: Bootstrap-Write of an Access Control
// instance, then Bootstrap-Read /1 and /2, each answered 2.05. Unknown
// resources in the reply (a vendor resource, client-maintained /1/x/12)
// are kept, not errors. Object-level Create rights (/2 instance with res 1
// = 65535 and owner 65535) and the default ACL instance 0 are written
// during bootstrap.
// Proves: BS-06, BS-25, BS-26, BS-30
func TestInt8BootstrapRead(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coap://s.example.com", "id", "key")
	cfg.ACLs = map[uint16]ACLConfig{
		0: {ObjectID: 3, ObjectInstanceID: 0, ACLs: map[uint16]uint16{0: 31}, Owner: 65535},
		1: {ObjectID: 5, ObjectInstanceID: 65535, ACLs: map[uint16]uint16{1: 16}, Owner: 65535},
	}
	cfg.Read = []string{"/1", "/2"}
	h.put("ep", cfg)
	c := h.client("ep", testclient.Config{})
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Code == codes.GET && r.Path == "/1" {
			// /1/0 as written plus /1/0/12 Last Bootstrapped and vendor resource 4242.
			nodes := append(ServerConfig{ShortID: 1, Lifetime: 86400}.nodes(p("/1/0")),
				lwm2m.ValueNode(p("/1/0/12"), lwm2m.Time(1700000000)),
				lwm2m.ValueNode(p("/1/0/4242"), lwm2m.String("vendor")))
			cd, _ := codec.For(lwm2m.FormatSenMLCBOR)
			body, _ := cd.Encode(p("/1"), nodes)
			return codes.Content, cfp(lwm2m.FormatSenMLCBOR), body, true
		}
		return 0, nil, nil, false
	})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if got, want := methods(c.Requests()), "PUT /0/0, PUT /1/0, PUT /2/0, PUT /2/1, GET /1, GET /2, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
	if v, _ := c.Get(p("/2/0/2/0")); v.Int != 31 {
		t.Fatalf("/2/0/2/0 = %v\n%s", v, c.Dump())
	}
	if v, _ := c.Get(p("/2/1/1")); v.Int != 65535 {
		t.Fatal("/2/1/1 not 65535")
	}
	found := map[string]bool{}
	for _, n := range res.Reads["/1"] {
		found[n.Path.String()] = true
	}
	if !found["/1/0/12"] || !found["/1/0/4242"] {
		t.Fatalf("read /1 lost resources: %v", res.Reads["/1"])
	}
	if len(res.Reads["/2"]) == 0 {
		t.Fatal("read /2 empty")
	}
}

// A Bootstrap-Read answered 4.06 (T Tbl 6.7-1) is retried with the next
// Accept; other error codes are recorded and the session goes on.
// Proves: BS-06
func TestReadNotAcceptableFallback(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coap://s.example.com", "id", "key")
	cfg.Read = []string{"/2/9", "/1/0"}
	h.put("ep", cfg)
	c := h.client("ep", testclient.Config{Formats: []lwm2m.ContentFormat{lwm2m.FormatTLV, lwm2m.FormatSenMLJSON}})
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Code == codes.GET && *r.Accept != lwm2m.FormatSenMLJSON {
			return codes.NotAcceptable, nil, nil, true
		}
		return 0, nil, nil, false
	})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	var reads []string
	for _, s := range res.Steps {
		if s.Code == codes.GET {
			reads = append(reads, s.Path.String()+":"+server.CodeString(s.Result))
		}
	}
	want := "/2/9:4.06, /2/9:4.06, /2/9:4.04, /1/0:4.06, /1/0:4.06, /1/0:2.05"
	if strings.Join(reads, ", ") != want {
		t.Fatalf("reads %v, want %s", reads, want)
	}
	if len(res.Reads["/1/0"]) == 0 {
		t.Fatal("/1/0 not decoded")
	}
}

// ETS 1.1-int-9 Bootstrap and Configuration Consistency: only /1/x/5 is
// written; the client answers Finish with 4.06 and the BS reports the
// failure. Client-Initiated Bootstrap reused on a bootstrapped client to
// update one resource (BS-22) succeeds.
// Proves: BS-07, BS-22
func TestInt9ConfigurationConsistency(t *testing.T) {
	h := newHarness(t)
	partial := &BootstrapConfig{Writes: []Write{{Path: p("/1/0/5"), Nodes: []lwm2m.Node{lwm2m.ValueNode(p("/1/0/5"), lwm2m.Integer(86400))}}}}
	h.put("ep", partial)
	c := h.client("ep", testclient.Config{})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.NotAcceptable || !errors.Is(res.Err, ErrFinishRejected) {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if got, want := methods(c.Requests()), "PUT /1/0/5, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}

	// Reuse after a full bootstrap: the account exists, the partial update passes.
	h.put("ep", c1("coap://s.example.com", "id", "key"))
	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("full: %s %v", server.CodeString(fin), res.Err)
	}
	h.put("ep", partial)
	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("partial update: %s %v", server.CodeString(fin), res.Err)
	}
	if v, _ := c.Get(p("/1/0/5")); v.Int != 86400 {
		t.Fatal("/1/0/5 not written")
	}
	if v, _ := c.Get(p("/1/0/1")); v.Int != 86400 {
		t.Fatal("resource write replaced the instance")
	}
}

// Replacing the BS account (C §6.1.3.3): writing a second BS account
// next to the client's makes the client answer Finish 4.06, and the old
// account stays active; with autoIdForSecurityObject the BS discovers the
// client's BS account (the /0 instance without ssid) first and writes
// over it, keeping the other /0 instances off its ID.
// Proves: BS-18, BS-16
func TestReplaceBootstrapAccount(t *testing.T) {
	h := newHarness(t)
	cfg := c1("coap://s.example.com", "id", "key")
	cfg.Security[1] = SecurityConfig{URI: "coaps://bs2.example.com", BootstrapServer: true, SecurityMode: ModePSK,
		PublicKeyOrID: Bytes("bs"), SecretKey: Bytes("bskey")}
	cfg.Security[0], cfg.Security[1] = cfg.Security[1], cfg.Security[0] // BS account at 0, server at 1
	h.put("ep", cfg)
	c := testclient.NewBootstrap(testclient.Config{Endpoint: "ep"})
	c.Set(p("/0/10/0"), lwm2m.String("coaps://bs.example.com"))
	c.Set(p("/0/10/1"), lwm2m.Boolean(true))
	c.Set(p("/0/10/2"), lwm2m.Integer(3))
	c.Set(p("/3/0/0"), lwm2m.String("OMA"))
	if err := c.Dial(h.udp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.NotAcceptable || !errors.Is(res.Err, ErrFinishRejected) {
		t.Fatalf("two BS accounts: finish %s err %v", server.CodeString(fin), res.Err)
	}
	if v, _ := c.Get(p("/0/10/0")); v.Str != "coaps://bs.example.com" {
		t.Fatal("old BS account not active")
	}

	cfg.AutoIDForSecurityObject = true
	h.put("ep", cfg)
	c.Set(p("/0/0/1"), lwm2m.Boolean(false)) // undo the stray account of the failed run
	c.Set(p("/0/0/10"), lwm2m.Integer(9))
	n := len(c.Requests())
	fin, res = h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("auto id: finish %s err %v\n%s", server.CodeString(fin), res.Err, c.Dump())
	}
	if got, want := methods(c.Requests()[n:]), "GET /, PUT /0/10, PUT /0/1, PUT /1/0, POST /bs"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
	if v, _ := c.Get(p("/0/10/0")); v.Str != "coaps://bs2.example.com" {
		t.Fatal("BS account not replaced in place")
	}
}

// Config rules the BS enforces before it provisions anything.
// Proves: BS-04, BS-06, BS-16, BS-17, BS-25, BS-28
func TestConfigValidation(t *testing.T) {
	ok := func() *BootstrapConfig { return c1("coaps://s.example.com:5684", "id", "key") }
	for name, mod := range map[string]func(*BootstrapConfig){
		"ssid 0 in /0":     func(c *BootstrapConfig) { s := c.Security[0]; s.ServerID = u16(0); c.Security[0] = s },
		"ssid 65535 in /0": func(c *BootstrapConfig) { s := c.Security[0]; s.ServerID = u16(65535); c.Security[0] = s },
		"no ssid in /0":    func(c *BootstrapConfig) { s := c.Security[0]; s.ServerID = nil; c.Security[0] = s },
		"ssid 0 in /1":     func(c *BootstrapConfig) { c.Servers[0] = ServerConfig{ShortID: 0} },
		"ssid 65535 in /1": func(c *BootstrapConfig) { c.Servers[0] = ServerConfig{ShortID: 65535} },
		"two BS accounts": func(c *BootstrapConfig) {
			c.Security[5] = SecurityConfig{URI: "coap://a", BootstrapServer: true}
			c.Security[6] = SecurityConfig{URI: "coap://b", BootstrapServer: true}
		},
		"URI too long": func(c *BootstrapConfig) {
			s := c.Security[0]
			s.URI = "coap://" + strings.Repeat("a", 250)
			c.Security[0] = s
		},
		"URI without scheme": func(c *BootstrapConfig) { s := c.Security[0]; s.URI = "server.example.com"; c.Security[0] = s },
		"create ACL owner": func(c *BootstrapConfig) {
			c.ACLs = map[uint16]ACLConfig{0: {ObjectID: 3, ObjectInstanceID: 65535, Owner: 1}}
		},
		"ACL owner 0":       func(c *BootstrapConfig) { c.ACLs = map[uint16]ACLConfig{0: {ObjectID: 3, Owner: 0}} },
		"read /3":           func(c *BootstrapConfig) { c.Read = []string{"/3"} },
		"read /1/0/1":       func(c *BootstrapConfig) { c.Read = []string{"/1/0/1"} },
		"delete a resource": func(c *BootstrapConfig) { c.ToDelete = []string{"/1/0/1"} },
		"text format":       func(c *BootstrapConfig) { c.ContentFormat = cfp(lwm2m.FormatText) },
		"raw write outside path": func(c *BootstrapConfig) {
			c.Writes = []Write{{p("/1/0"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/1"), lwm2m.String("x"))}}}
		},
	} {
		c := ok()
		mod(c)
		if err := NewMemoryConfigStore().Put("ep", c); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: %v, want invalid", name, err)
		}
	}
	good := ok()
	good.Read = []string{"/1", "/2/0"}
	good.ToDelete = []string{"/", "/0", "/1/0"}
	good.ACLs = map[uint16]ACLConfig{0: {ObjectID: 3, ObjectInstanceID: 65535, Owner: 65535}}
	s := good.Security[0]
	s.URI = "coaps://" + strings.Repeat("a", 247) // 255 characters
	good.Security[0] = s
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

// /0 instance content per security mode (T §5.2.9): null resources are
// omitted, the RECOMMENDED form. /0/x/12 BS-Account Timeout is written
// when configured; /0/x/17 links the OSCORE instance.
// Proves: BS-24, BS-19
func TestSecurityResourcesPerMode(t *testing.T) {
	ids := func(ns []lwm2m.Node) string {
		var out []string
		for _, n := range ns {
			out = append(out, n.Path.String()[len("/0/0/"):])
		}
		return strings.Join(out, ",")
	}
	for mode, want := range map[SecurityMode]string{
		ModeNoSec: "0,1,2",
		ModePSK:   "0,1,2,3,5",
		ModeRPK:   "0,1,2,3,4,5",
		ModeX509:  "0,1,2,3,4,5",
		ModeEST:   "0,1,2,4",
	} {
		s := SecurityConfig{URI: "coap://s", SecurityMode: mode, PublicKeyOrID: Bytes("a"), ServerPublicKey: Bytes("b"), SecretKey: Bytes("c")}
		if got := ids(s.nodes(p("/0/0"))); got != want {
			t.Errorf("mode %d: resources %s, want %s", mode, got, want)
		}
	}
	bs := SecurityConfig{URI: "coaps://bs", BootstrapServer: true, SecurityMode: ModeNoSec,
		BootstrapServerAccountTimeout: u32p(0), ClientOldOffTime: u32p(1), OSCORE: u16(0)}
	ns := bs.nodes(p("/0/0"))
	if got := ids(ns); got != "0,1,2,11,12,17" {
		t.Fatalf("resources %s", got)
	}
	if v := ns[len(ns)-1].Value; v.Type != lwm2m.TypeObjlnk || v.Link != (lwm2m.ObjLink{Object: 21, Instance: 0}) {
		t.Fatalf("/0/0/17 = %v", v)
	}
}

// The Leshan BootstrapConfig JSON the Zephyr harness posts
// (zephyr-interop §4.3) decodes into the config model and provisions the
// expected end state (§4.4).
func TestLeshanConfigJSON(t *testing.T) {
	ascii := func(s string) string { b, _ := json.Marshal(Bytes(s)); return string(b) }
	body := `{"servers":{"0":{"binding":"U","defaultMinPeriod":1,"lifetime":86400,"notifIfDisabled":false,"shortId":1}},
 "security":{"1":{"bootstrapServer":false,"clientOldOffTime":1,"publicKeyOrId":` + ascii("ep1") + `,
   "secretKey":` + ascii("pass") + `,"securityMode":"PSK","serverId":1,"serverSmsNumber":"",
   "smsBindingKeyParam":[],"smsBindingKeySecret":[],"smsSecurityMode":"NO_SEC","uri":"coaps://192.0.2.2:5684"}},
 "oscore":{},"toDelete":["/0","/1"]}`
	var cfg BootstrapConfig
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	dels, writes := cfg.plan(nil, false)
	if len(dels) != 2 || len(writes) != 2 || writes[0].Path != p("/0/1") || writes[1].Path != p("/1/0") {
		t.Fatalf("plan %v %v", dels, writes)
	}
	got := lwm2m.FormatNodes(writes[0].Nodes)
	for _, want := range []string{"/0/1/3=", "/0/1/10=1", "/0/1/11=1", "coaps://192.0.2.2:5684"} {
		if !strings.Contains(got, want) {
			t.Fatalf("security %s lacks %s", got, want)
		}
	}
	if string(cfg.Security[1].PublicKeyOrID) != "ep1" || string(cfg.Security[1].SecretKey) != "pass" {
		t.Fatal("byte arrays")
	}
	out, err := json.Marshal(cfg.Security[1])
	if err != nil || !strings.Contains(string(out), `"publicKeyOrId":[101,112,49]`) || !strings.Contains(string(out), `"securityMode":"PSK"`) {
		t.Fatalf("marshal %s %v", out, err)
	}
}
