//go:build interop

package peers

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Wakaama's example client: SSID 123, test object 31024 with instances 10-12
// (res 1 int "test" = 20+i, 2 exec, 3 float), Device /3/0, a /5 stub.
const wkSSID = "123"

func startWakaama(t *testing.T, e *env, ep string, extra ...string) *proc {
	t.Helper()
	args := append([]string{"-4", "-h", "127.0.0.1", "-p", strconv.Itoa(e.udp), "-l", "0", "-n", ep, "-t", "60"}, extra...)
	return start(t, bin(t, "WAKAAMA_CLIENT"), args...)
}

func TestWakaamaRegistration(t *testing.T) {
	e := newEnv(t)
	c := startWakaama(t, e, "wk-reg")
	reg := e.registered("wk-reg")
	if reg.Version != "1.1" || reg.Lifetime != 60*time.Second || reg.Binding != "U" {
		t.Errorf("registration: version %q lifetime %v binding %q", reg.Version, reg.Lifetime, reg.Binding)
	}
	for _, in := range [][2]uint16{{3, 0}, {5, 0}, {31024, 10}, {31024, 12}} {
		if !reg.HasInstance(in[0], in[1]) {
			t.Errorf("object list %q lacks /%d/%d", reg.RawLinks, in[0], in[1])
		}
	}
	if reg.HasObject(0) {
		t.Errorf("/0 in the object list: %q", reg.RawLinks)
	}
	// T17: Wakaama's Update after an object-set change carries no CF;
	// "update" sends a plain Update with no query and no payload.
	c.send("update " + wkSSID)
	up := e.ev.wait(t, evTimeout, "Update", isUpdated("wk-reg")).(server.Updated)
	if up.Registration.ID != reg.ID || up.Registration.Lifetime != 60*time.Second {
		t.Errorf("update changed the registration: %+v", up.Registration)
	}
	c.send("quit")
	d := e.ev.wait(t, evTimeout, "De-register", isDeregistered("wk-reg")).(server.Deregistered)
	if d.Reason != server.ReasonDeregistered {
		t.Errorf("deregister reason %v", d.Reason)
	}
	if _, ok := e.srv.Store().ByEndpoint("wk-reg"); ok {
		t.Error("registration still stored after De-register")
	}
}

func TestWakaamaPSK(t *testing.T) {
	e := newEnv(t)
	key := []byte("0123456789abcdef")
	e.putPSK("wk-psk", "wk-psk-id", key)
	start(t, bin(t, "WAKAAMA_CLIENT_DTLS"), "-4", "-h", "127.0.0.1", "-p", strconv.Itoa(e.dtls), "-l", "0",
		"-n", "wk-psk", "-t", "60", "-i", "wk-psk-id", "-s", hexKey(key))
	reg := e.registered("wk-psk")
	if reg.Identity.Mode != server.ModePSK || reg.Identity.PSKIdentity != "wk-psk-id" {
		t.Fatalf("identity %+v", reg.Identity)
	}
	r := ok(t, "read over DTLS")(e.srv.Read(e.ctx, "wk-psk", p("/3/0/0"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if got := string(r.Payload); got != "Open Mobile Alliance" {
		t.Errorf("/3/0/0 = %q", got)
	}
}

func TestWakaamaPSKWrongKey(t *testing.T) {
	e := newEnv(t)
	e.putPSK("wk-bad", "wk-bad-id", []byte("0123456789abcdef"))
	start(t, bin(t, "WAKAAMA_CLIENT_DTLS"), "-4", "-h", "127.0.0.1", "-p", strconv.Itoa(e.dtls), "-l", "0",
		"-n", "wk-bad", "-t", "60", "-i", "wk-bad-id", "-s", hexKey([]byte("fedcba9876543210")))
	e.ev.none(t, 8*time.Second, "registration with a wrong PSK", isRegistered("wk-bad"))
}

func TestWakaamaDeviceManagement(t *testing.T) {
	e := newEnv(t)
	startWakaama(t, e, "wk-dm")
	e.registered("wk-dm")
	ep := "wk-dm"
	stable := []string{"/3/0/0", "/3/0/1", "/3/0/2", "/3/0/3"}

	// Read /3/0 in every format the client encodes (SenML CBOR is off in the
	// example build); all must decode to the same values.
	var ref []lwm2m.Node
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatTLV, lwm2m.FormatSenMLJSON, lwm2m.FormatOMAJSON} {
		r := ok(t, "read /3/0 "+f.String())(e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{Accept: cf(f)}))
		if r.ContentFormat != f || r.DecodeErr != nil {
			t.Fatalf("read /3/0 %v: got CF %v, decode %v", f, r.ContentFormat, r.DecodeErr)
		}
		got := pick(r.Nodes, stable...)
		if len(got) != len(stable) {
			t.Fatalf("read /3/0 %v: %s", f, lwm2m.FormatNodes(r.Nodes))
		}
		if ref == nil {
			ref = got
		} else if !sameNodes(ref, got) {
			t.Errorf("%v disagrees with TLV:\n%s\n%s", f, lwm2m.FormatNodes(got), lwm2m.FormatNodes(ref))
		}
	}
	if v := value(t, ref, "/3/0/0"); v.Str != "Open Mobile Alliance" {
		t.Errorf("manufacturer %v", v)
	}
	// No Accept: T34, Wakaama picks text for a resource, SenML JSON otherwise.
	r := ok(t, "read /3/0/9")(e.srv.Read(e.ctx, ep, p("/3/0/9"), server.ReadOptions{}))
	if len(r.Nodes) != 1 || r.Nodes[0].Path != p("/3/0/9") {
		t.Errorf("read /3/0/9 without Accept: CF %v nodes %s", r.ContentFormat, lwm2m.FormatNodes(r.Nodes))
	}
	r = ok(t, "read /3/0 no Accept")(e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{}))
	if !sameNodes(pick(r.Nodes, stable...), ref) {
		t.Errorf("read /3/0 without Accept (CF %v): %s", r.ContentFormat, lwm2m.FormatNodes(r.Nodes))
	}

	// Write in each format, read back as text.
	res := p("/31024/10/1")
	for i, f := range []lwm2m.ContentFormat{lwm2m.FormatText, lwm2m.FormatTLV, lwm2m.FormatSenMLJSON, lwm2m.FormatOMAJSON} {
		want := int64(100 + i)
		ok(t, "write "+f.String())(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(want))},
			server.WriteOptions{Format: cf(f)}))
		r := ok(t, "read back")(e.srv.Read(e.ctx, ep, res, server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
		if string(r.Payload) != strconv.FormatInt(want, 10) {
			t.Errorf("after %v write: %q, want %d", f, r.Payload, want)
		}
	}
	// Partial Update of an instance (POST, TLV): float resource 3.
	ok(t, "partial update")(e.srv.Write(e.ctx, ep, p("/31024/11"),
		[]lwm2m.Node{lwm2m.ValueNode(p("/31024/11/3"), lwm2m.Float(2.5))}, server.WriteOptions{Mode: server.PartialUpdate}))
	// Object 31024 has no model here, so read back in SenML JSON (typed on
	// the wire), not TLV (untyped bytes without a schema).
	r = ok(t, "read /31024/11")(e.srv.Read(e.ctx, ep, p("/31024/11"), server.ReadOptions{Accept: cf(lwm2m.FormatSenMLJSON)}))
	if v := value(t, r.Nodes, "/31024/11/3"); v.Float != 2.5 {
		t.Errorf("/31024/11/3 = %v", v)
	}
	if v := value(t, r.Nodes, "/31024/11/1"); plain(v) != "21" {
		t.Errorf("partial update touched /31024/11/1: %v", v)
	}
	// Write to a read-only resource is refused by the client, reported as is.
	r, err := e.srv.Write(e.ctx, ep, p("/3/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("x"))},
		server.WriteOptions{Format: cf(lwm2m.FormatText)})
	if err != nil || r.Success() {
		t.Errorf("write read-only /3/0/0: %v %v", r, err)
	}

	ok(t, "execute")(e.srv.Execute(e.ctx, ep, p("/31024/10/2"), ""))

	// Create triggers an Update with the new instance (T38: Wakaama does).
	code(t, "create", codes.Created)(e.srv.Create(e.ctx, ep, p("/31024"), []lwm2m.Node{
		lwm2m.ValueNode(p("/31024/20/1"), lwm2m.Integer(7)), lwm2m.ValueNode(p("/31024/20/3"), lwm2m.Float(1.5))}, nil))
	up := e.ev.wait(t, evTimeout, "Update after Create", isUpdated(ep)).(server.Updated)
	if !up.Registration.HasInstance(31024, 20) {
		t.Errorf("update object list %q lacks /31024/20", up.Registration.RawLinks)
	}
	r = ok(t, "read created")(e.srv.Read(e.ctx, ep, p("/31024/20/1"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != "7" {
		t.Errorf("created /31024/20/1 = %q", r.Payload)
	}
	code(t, "delete", codes.Deleted)(e.srv.Delete(e.ctx, ep, p("/31024/20")))
	up = e.ev.wait(t, evTimeout, "Update after Delete", isUpdated(ep)).(server.Updated)
	if up.Registration.HasInstance(31024, 20) {
		t.Errorf("update object list %q still has /31024/20", up.Registration.RawLinks)
	}
	r, err = e.srv.Read(e.ctx, ep, p("/31024/20"), server.ReadOptions{})
	code(t, "read deleted", codes.NotFound)(r, err)

	// Discover.
	r = ok(t, "discover")(e.srv.Discover(e.ctx, ep, p("/31024/10"), nil))
	links, err := link.Parse(string(r.Payload))
	if err != nil {
		t.Fatalf("discover payload %q: %v", r.Payload, err)
	}
	entries, err := link.ParseDiscover(links, "")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, en := range entries {
		paths = append(paths, en.Path.String())
	}
	if !strings.Contains(strings.Join(paths, " "), "/31024/10/1") {
		t.Errorf("discover /31024/10: %q", r.Payload)
	}
}

func TestWakaamaObserve(t *testing.T) {
	e := newEnv(t)
	startWakaama(t, e, "wk-obs")
	e.registered("wk-obs")
	ep, res := "wk-obs", p("/31024/10/1")

	// T42: attributes on the exact observed path, in one Write-Attributes.
	ok(t, "write-attributes")(e.srv.WriteAttributes(e.ctx, ep, res, []string{"pmin=0", "pmax=2"}))
	ob, r, err := e.srv.Observe(e.ctx, ep, res, server.ObserveOptions{Accept: cf(lwm2m.FormatText)})
	ok(t, "observe")(r, err)
	if string(r.Payload) != "20" {
		t.Errorf("observe initial %q", r.Payload)
	}
	// A change notifies with the new value.
	ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(55))},
		server.WriteOptions{Format: cf(lwm2m.FormatText)}))
	waitValue(t, e, ob, "/31024/10/1", "55")
	// pmax=2: notifications keep coming without a change.
	waitValue(t, e, ob, "/31024/10/1", "55")
	waitValue(t, e, ob, "/31024/10/1", "55")

	ok(t, "cancel")(e.srv.CancelObservation(e.ctx, ob, true))
	time.Sleep(500 * time.Millisecond)
	for e.ev.take(isNotification(ob)) != nil { // drain in-flight ones
	}
	ok(t, "write after cancel")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(56))},
		server.WriteOptions{Format: cf(lwm2m.FormatText)}))
	e.ev.none(t, 5*time.Second, "notification after cancel", isNotification(ob))
}

// waitValue waits for a notification of ob carrying path = want.
func waitValue(t *testing.T, e *env, ob *server.Observation, path, want string) server.Notification {
	t.Helper()
	deadline := time.Now().Add(evTimeout)
	for time.Now().Before(deadline) {
		n := e.ev.wait(t, time.Until(deadline), "notification "+path+"="+want, isNotification(ob)).(server.Notification)
		for _, nd := range n.Response.Nodes {
			if nd.Path.String() == path && plain(nd.Value) == want {
				return n
			}
		}
		t.Logf("notification: CF %v %q nodes %s err %v", n.Response.ContentFormat, n.Response.Payload,
			lwm2m.FormatNodes(n.Response.Nodes), n.Response.DecodeErr)
	}
	t.Fatalf("no notification %s=%s", path, want)
	return server.Notification{}
}

func TestWakaamaSend(t *testing.T) {
	e := newEnv(t)
	c := startWakaama(t, e, "wk-send")
	e.registered("wk-send")
	// Mute Send defaults to true when /1/x/23 is missing (Wakaama); the
	// example's Server object has it, so set it false explicitly.
	ok(t, "unmute")(e.srv.Write(e.ctx, "wk-send", p("/1/0/23"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/23"), lwm2m.Boolean(false))},
		server.WriteOptions{Format: cf(lwm2m.FormatText)}))
	c.send("send " + wkSSID + " /3/0/9 /31024/10/1")
	s := e.ev.wait(t, evTimeout, "Send", isSend("wk-send")).(server.SendReceived)
	if v := value(t, s.Nodes, "/31024/10/1"); plain(v) != "20" {
		t.Errorf("sent /31024/10/1 = %v", v)
	}
	if !hasNode(s.Nodes, "/3/0/9") {
		t.Errorf("send lacks /3/0/9: %s", lwm2m.FormatNodes(s.Nodes))
	}
}

func TestWakaamaBootstrap(t *testing.T) {
	e := newEnv(t)
	b := newBS(t)
	ssid := uint16(123)
	if err := b.configs.Put("wk-bs", &bootstrap.BootstrapConfig{
		ToDelete: []string{"/0", "/1"},
		// Never write over the client's own BS account (Anjay keeps it at /0/1).
		AutoIDForSecurityObject: true,
		Security:                map[uint16]bootstrap.SecurityConfig{1: {URI: uri("coap", e.udp), SecurityMode: bootstrap.ModeNoSec, ServerID: &ssid}},
		Servers:                 map[uint16]bootstrap.ServerConfig{1: {ShortID: ssid, Lifetime: 45, Binding: "U"}},
		Discover:                true,
	}); err != nil {
		t.Fatal(err)
	}
	start(t, bin(t, "WAKAAMA_CLIENT"), "-4", "-b", "-h", "127.0.0.1", "-p", strconv.Itoa(b.udp), "-l", "0", "-n", "wk-bs")
	res := b.result(t) // ~10 s: the example's Client Hold Off Time /0/x/11
	if res.Err != nil || res.Pack {
		t.Fatalf("bootstrap: err %v pack %v steps %+v", res.Err, res.Pack, res.Steps)
	}
	if len(res.Discover) == 0 {
		t.Error("Bootstrap-Discover returned nothing")
	}
	reg := e.registered("wk-bs")
	if reg.Lifetime != 45*time.Second {
		t.Errorf("lifetime %v, want the bootstrapped 45s", reg.Lifetime)
	}
	r := ok(t, "read /1/1/1")(e.srv.Read(e.ctx, "wk-bs", p("/1/1/1"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != "45" {
		t.Errorf("/1/1/1 = %q", r.Payload)
	}
}

func TestWakaamaFirmwarePush(t *testing.T) {
	e := newEnv(t)
	startWakaama(t, e, "wk-fw")
	e.registered("wk-fw")
	ep := "wk-fw"
	// /5/0/9 Delivery Method: the example supports push only (1).
	r := ok(t, "read /5/0/9")(e.srv.Read(e.ctx, ep, p("/5/0/9"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != "1" {
		t.Errorf("delivery method %q", r.Payload)
	}
	// A 2000-byte package goes Block1 (RFC 7959) in two 1024-byte blocks.
	// Wakaama reassembles at most WAKAAMA_COAP_MAX_MESSAGE_SIZE (2048) bytes
	// and answers 4.13 beyond that (client limit, see README).
	pkg := bytes.Repeat([]byte("wakaama-fw"), 200)
	ok(t, "write package")(e.srv.Write(e.ctx, ep, p("/5/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/5/0/0"), lwm2m.Opaque(pkg))},
		server.WriteOptions{Format: cf(lwm2m.FormatOpaque)}))
	ok(t, "execute update")(e.srv.Execute(e.ctx, ep, p("/5/0/2"), ""))
	// The stub moves State 1 -> 2 on Update.
	r = ok(t, "read state")(e.srv.Read(e.ctx, ep, p("/5/0/3"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != "2" {
		t.Errorf("state after update %q", r.Payload)
	}
}

// T32: Wakaama has no FETCH/iPATCH; it ACKs and never answers. The server
// must fail the composite request within the caller's bound and keep the
// client usable.
func TestWakaamaCompositeUnsupported(t *testing.T) {
	e := newEnv(t)
	startWakaama(t, e, "wk-comp")
	e.registered("wk-comp")
	ctx, cancel := context.WithTimeout(e.ctx, 8*time.Second)
	defer cancel()
	r, err := e.srv.ReadComposite(ctx, "wk-comp", []lwm2m.Path{p("/3/0/0"), p("/31024/10/1")}, server.CompositeOptions{})
	if err == nil && r.Success() {
		t.Fatalf("composite read succeeded on Wakaama: %+v", r)
	}
	t.Logf("composite read: %v %v", r, err)
	if v := readText(t, e, "wk-comp", "/3/0/0"); v != "Open Mobile Alliance" {
		t.Errorf("read after composite: %q", v)
	}
}
