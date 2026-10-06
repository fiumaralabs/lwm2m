//go:build interop

package peers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// The Anjay demo client: SSID 1, test object 33605 (no instances at start;
// res 1 counter, 2 increment-counter exec, 10 raw bytes, 12 int, 13 bool,
// 14 float, 15 string, 18 double), Device /3/0, Firmware Update /5/0.

func startAnjay(t *testing.T, ep, serverURI string, extra ...string) *proc {
	t.Helper()
	dir := t.TempDir()
	args := append([]string{"-e", ep, "-u", serverURI, "-l", "60",
		"--fw-updated-marker-path", filepath.Join(dir, "fw-updated")}, extra...)
	return start(t, bin(t, "ANJAY_DEMO"), args...)
}

func TestAnjayRegistration(t *testing.T) {
	for _, v := range []string{"1.0", "1.1", "1.2"} {
		t.Run("lwm2m="+v, func(t *testing.T) {
			e := newEnv(t)
			ep := "aj-reg-" + v
			c := startAnjay(t, ep, uri("coap", e.udp), "-v", v, "-V", v)
			reg := e.registered(ep)
			if reg.Version != v || reg.Lifetime != 60*time.Second {
				t.Errorf("version %q lifetime %v", reg.Version, reg.Lifetime)
			}
			// T4: Anjay omits b when the binding is U.
			if reg.Binding != "U" || reg.QueueMode {
				t.Errorf("binding %q queue %v", reg.Binding, reg.QueueMode)
			}
			// T11: no </> root link, so no ct; objects still parse.
			for _, o := range []uint16{1, 3, 5, 33605} {
				if !reg.HasObject(o) {
					t.Errorf("object list %q lacks /%d", reg.RawLinks, o)
				}
			}
			// T13: a 1.0 registration quotes ver="1.1"; every ver sent
			// must parse, quoted or not.
			for _, o := range reg.Objects {
				sent := strings.Contains(reg.RawLinks, "</"+strconv.Itoa(int(o.ID))+">;ver=")
				if sent && o.Version == "" {
					t.Errorf("/%d: ver in %q not parsed", o.ID, reg.RawLinks)
				}
			}
			if v == "1.0" && !strings.Contains(reg.RawLinks, `ver="`) {
				t.Logf("1.0 registration without a quoted ver: %q", reg.RawLinks)
			}
			c.send("send-update")
			up := e.ev.wait(t, evTimeout, "Update", isUpdated(ep)).(server.Updated)
			if up.Registration.ID != reg.ID {
				t.Errorf("update moved registration %s -> %s", reg.ID, up.Registration.ID)
			}
			c.closeStdin() // the demo de-registers on stdin EOF; SIGINT kills it
			d := e.ev.wait(t, evTimeout, "De-register", isDeregistered(ep)).(server.Deregistered)
			if d.Reason != server.ReasonDeregistered {
				t.Errorf("deregister reason %v", d.Reason)
			}
		})
	}
}

// anjayDM registers an Anjay client and creates test instance /33605/1.
func anjayDM(t *testing.T, e *env, ep, serverURI string, extra ...string) *proc {
	t.Helper()
	c := startAnjay(t, ep, serverURI, extra...)
	e.registered(ep)
	code(t, "create /33605/1", codes.Created)(e.srv.Create(e.ctx, ep, p("/33605"), []lwm2m.Node{
		lwm2m.ValueNode(p("/33605/1/12"), lwm2m.Integer(5)),
		lwm2m.ValueNode(p("/33605/1/15"), lwm2m.String("init")),
	}, cf(lwm2m.FormatSenMLCBOR)))
	return c
}

func TestAnjayDeviceManagement(t *testing.T) {
	e := newEnv(t)
	ep := "aj-dm"
	anjayDM(t, e, ep, uri("coap", e.udp))
	stable := []string{"/3/0/0", "/3/0/1", "/3/0/2", "/3/0/3"}

	// Read /3/0 in every format Anjay encodes; typed formats must agree.
	var ref []lwm2m.Node
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR,
		lwm2m.FormatTLV, lwm2m.FormatOMAJSON} {
		r := ok(t, "read /3/0 "+f.String())(e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{Accept: cf(f)}))
		if r.ContentFormat != f || r.DecodeErr != nil {
			t.Fatalf("read /3/0 %v: CF %v decode %v", f, r.ContentFormat, r.DecodeErr)
		}
		got := pick(r.Nodes, stable...)
		if len(got) != len(stable) {
			t.Fatalf("read /3/0 %v: %s", f, lwm2m.FormatNodes(r.Nodes))
		}
		if ref == nil {
			ref = got
		} else if !sameNodes(ref, got) {
			t.Errorf("%v disagrees with SenML CBOR:\n%s\n%s", f, lwm2m.FormatNodes(got), lwm2m.FormatNodes(ref))
		}
	}
	// No Accept: SenML CBOR for hierarchical data on 1.2, text for a
	// single resource (T34); both decode.
	r := ok(t, "read /3/0")(e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{}))
	if !sameNodes(pick(r.Nodes, stable...), ref) {
		t.Errorf("read /3/0 without Accept (CF %v): %s", r.ContentFormat, lwm2m.FormatNodes(r.Nodes))
	}
	r = ok(t, "read /3/0/0 text")(e.srv.Read(e.ctx, ep, p("/3/0/0"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != plain(value(t, ref, "/3/0/0")) {
		t.Errorf("text /3/0/0 = %q, SenML %v", r.Payload, value(t, ref, "/3/0/0"))
	}
	// CBOR (60) for a single resource.
	r = ok(t, "read cbor")(e.srv.Read(e.ctx, ep, p("/3/0/0"), server.ReadOptions{Accept: cf(lwm2m.FormatCBOR)}))
	if r.ContentFormat != lwm2m.FormatCBOR || len(r.Nodes) != 1 || plain(r.Nodes[0].Value) != plain(value(t, ref, "/3/0/0")) {
		t.Errorf("cbor /3/0/0: CF %v %s", r.ContentFormat, lwm2m.FormatNodes(r.Nodes))
	}

	// Write the int resource in every format Anjay decodes, read back.
	res := p("/33605/1/12")
	for i, f := range []lwm2m.ContentFormat{lwm2m.FormatText, lwm2m.FormatTLV, lwm2m.FormatCBOR, lwm2m.FormatSenMLJSON,
		lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR} {
		want := strconv.Itoa(1000 + i)
		n, _ := strconv.ParseInt(want, 10, 64)
		ok(t, "write "+f.String())(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(n))},
			server.WriteOptions{Format: cf(f)}))
		r := ok(t, "read back")(e.srv.Read(e.ctx, ep, res, server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
		if string(r.Payload) != want {
			t.Errorf("after %v write: %q, want %s", f, r.Payload, want)
		}
	}
	// Opaque (42) on the raw-bytes resource, round trip byte for byte.
	raw := []byte{0, 1, 2, 0xfe, 0xff, 'a'}
	ok(t, "write opaque")(e.srv.Write(e.ctx, ep, p("/33605/1/10"), []lwm2m.Node{lwm2m.ValueNode(p("/33605/1/10"), lwm2m.Opaque(raw))},
		server.WriteOptions{Format: cf(lwm2m.FormatOpaque)}))
	r = ok(t, "read opaque")(e.srv.Read(e.ctx, ep, p("/33605/1/10"), server.ReadOptions{Accept: cf(lwm2m.FormatOpaque)}))
	if !bytes.Equal(r.Payload, raw) {
		t.Errorf("opaque round trip %x", r.Payload)
	}
	// Block1 write and Block2 read (RFC 7959): 5000 opaque bytes.
	big := bytes.Repeat([]byte{0xA5, 0x5A, 0x00, 0xFF}, 1250)
	ok(t, "block1 write")(e.srv.Write(e.ctx, ep, p("/33605/1/10"), []lwm2m.Node{lwm2m.ValueNode(p("/33605/1/10"), lwm2m.Opaque(big))},
		server.WriteOptions{Format: cf(lwm2m.FormatOpaque)}))
	r = ok(t, "block2 read")(e.srv.Read(e.ctx, ep, p("/33605/1/10"), server.ReadOptions{Accept: cf(lwm2m.FormatOpaque)}))
	if !bytes.Equal(r.Payload, big) {
		t.Errorf("block-wise round trip: %d bytes back, want %d", len(r.Payload), len(big))
	}
	// Replace of an instance with typed values in LwM2M CBOR, then a
	// Partial Update that must keep the other resources.
	ok(t, "replace instance")(e.srv.Write(e.ctx, ep, p("/33605/1"), []lwm2m.Node{
		lwm2m.ValueNode(p("/33605/1/12"), lwm2m.Integer(-7)), lwm2m.ValueNode(p("/33605/1/13"), lwm2m.Boolean(true)),
		lwm2m.ValueNode(p("/33605/1/15"), lwm2m.String("replaced")), lwm2m.ValueNode(p("/33605/1/18"), lwm2m.Float(0.125)),
	}, server.WriteOptions{Format: cf(lwm2m.FormatLwM2MCBOR)}))
	ok(t, "partial update")(e.srv.Write(e.ctx, ep, p("/33605/1"), []lwm2m.Node{lwm2m.ValueNode(p("/33605/1/15"), lwm2m.String("partial"))},
		server.WriteOptions{Mode: server.PartialUpdate, Format: cf(lwm2m.FormatSenMLJSON)}))
	r = ok(t, "read instance")(e.srv.Read(e.ctx, ep, p("/33605/1"), server.ReadOptions{Accept: cf(lwm2m.FormatLwM2MCBOR)}))
	for path, want := range map[string]string{"/33605/1/12": "-7", "/33605/1/13": "true", "/33605/1/15": "partial", "/33605/1/18": "0.125"} {
		if got := plain(value(t, r.Nodes, path)); got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}

	// Execute increments the counter.
	before := readText(t, e, ep, "/33605/1/1")
	ok(t, "execute")(e.srv.Execute(e.ctx, ep, p("/33605/1/2"), ""))
	if after := readText(t, e, ep, "/33605/1/1"); after != strconv.Itoa(atoi(before)+1) {
		t.Errorf("counter %s -> %s after execute", before, after)
	}
	// Execute on a non-executable resource: 4.05.
	code(t, "execute /3/0/0", codes.MethodNotAllowed)(e.srv.Execute(e.ctx, ep, p("/3/0/0"), ""))

	// Create in TLV with a client-visible IID, Delete, then 4.04.
	code(t, "create tlv", codes.Created)(e.srv.Create(e.ctx, ep, p("/33605"), []lwm2m.Node{
		lwm2m.ValueNode(p("/33605/7/12"), lwm2m.Integer(70))}, cf(lwm2m.FormatTLV)))
	if v := readText(t, e, ep, "/33605/7/12"); v != "70" {
		t.Errorf("created /33605/7/12 = %s", v)
	}
	// T36: an existing IID is 4.00 on Anjay.
	r, err := e.srv.Create(e.ctx, ep, p("/33605"), []lwm2m.Node{lwm2m.ValueNode(p("/33605/7/12"), lwm2m.Integer(1))}, cf(lwm2m.FormatTLV))
	if err != nil || r.Success() {
		t.Errorf("create of an existing instance: %v %v", r, err)
	}
	code(t, "delete", codes.Deleted)(e.srv.Delete(e.ctx, ep, p("/33605/7")))
	code(t, "read deleted", codes.NotFound)(e.srv.Read(e.ctx, ep, p("/33605/7"), server.ReadOptions{}))

	// Discover with the 1.2 depth modifier.
	for depth, want := range map[int][2]bool{0: {false, false}, 1: {true, false}, 2: {true, true}} {
		d := depth
		paths := discover(t, e, ep, "/33605", &d)
		gotInst, gotRes := paths["/33605/1"], paths["/33605/1/12"]
		if !paths["/33605"] || gotInst != want[0] || gotRes != want[1] {
			t.Errorf("discover /33605 depth=%d: %v", depth, paths)
		}
	}
	paths := discover(t, e, ep, "/3/0", nil)
	if !paths["/3/0/0"] {
		t.Errorf("discover /3/0: %v", paths)
	}
}

func readText(t *testing.T, e *env, ep, path string) string {
	t.Helper()
	r := ok(t, "read "+path)(e.srv.Read(e.ctx, ep, p(path), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	return string(r.Payload)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func discover(t *testing.T, e *env, ep, path string, depth *int) map[string]bool {
	t.Helper()
	r := ok(t, "discover "+path)(e.srv.Discover(e.ctx, ep, p(path), depth))
	links, err := link.Parse(string(r.Payload))
	if err != nil {
		t.Fatalf("discover %q: %v", r.Payload, err)
	}
	entries, err := link.ParseDiscover(links, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, en := range entries {
		out[en.Path.String()] = true
	}
	return out
}

func TestAnjayObserve(t *testing.T) {
	res := p("/33605/1/12")
	var e *env
	var ep string
	fresh := func(t *testing.T) {
		e = newEnv(t)
		ep = "aj-obs-" + strings.ReplaceAll(t.Name(), "/", "-")
		// Anjay does not notify a server of changes that server made
		// itself unless self-notify is on (client policy, see README).
		anjayDM(t, e, ep, uri("coap", e.udp), "--enable-self-notify")
	}

	t.Run("write-attributes", func(t *testing.T) {
		fresh(t)
		ok(t, "write-attributes")(e.srv.WriteAttributes(e.ctx, ep, res, []string{"pmin=0", "pmax=1"}))
		ob, r, err := e.srv.Observe(e.ctx, ep, res, server.ObserveOptions{Accept: cf(lwm2m.FormatText)})
		ok(t, "observe")(r, err)
		if string(r.Payload) != "5" {
			t.Errorf("initial %q", r.Payload)
		}
		ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(41))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
		waitValue(t, e, ob, "/33605/1/12", "41")
		waitValue(t, e, ob, "/33605/1/12", "41") // pmax=1 without a change
		ok(t, "cancel")(e.srv.CancelObservation(e.ctx, ob, true))
		drain(e, ob)
		ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(42))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
		e.ev.none(t, 3*time.Second, "notification after cancel", isNotification(ob))
		ok(t, "clear attributes")(e.srv.WriteAttributes(e.ctx, ep, res, []string{"pmin", "pmax"}))
	})

	t.Run("attributes-in-observe", func(t *testing.T) {
		fresh(t)
		// 1.2: attributes in the Observe request (OBS-06); gt is an
		// edge condition, so only crossing values notify.
		ob, r, err := e.srv.Observe(e.ctx, ep, res, server.ObserveOptions{Accept: cf(lwm2m.FormatLwM2MCBOR), Query: []string{"gt=100"}})
		ok(t, "observe with gt")(r, err)
		for _, v := range []int64{50, 150} {
			ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(v))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
			time.Sleep(300 * time.Millisecond)
		}
		n := waitValue(t, e, ob, "/33605/1/12", "150")
		if n.Response.ContentFormat != lwm2m.FormatLwM2MCBOR {
			t.Errorf("notification CF %v, want the Observe Accept", n.Response.ContentFormat)
		}
		for e.ev.take(isNotification(ob)) != nil {
		}
		ok(t, "write below")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(60))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
		e.ev.none(t, 2*time.Second, "notification for a value that did not cross gt", func(ev server.Event) bool {
			n, ok := ev.(server.Notification)
			return ok && n.Observation.ID == ob.ID && hasNode(n.Response.Nodes, "/33605/1/12") &&
				plain(value(t, n.Response.Nodes, "/33605/1/12")) == "50"
		})
		ok(t, "cancel")(e.srv.CancelObservation(e.ctx, ob, true))
	})

	t.Run("confirmable", func(t *testing.T) {
		// con=1 (1.2): CON notifications. If we did not ACK them Anjay
		// would retransmit and then drop the observation; with pmax=1 they
		// keep coming.
		fresh(t)
		ob, r, err := e.srv.Observe(e.ctx, ep, res, server.ObserveOptions{Query: []string{"con=1", "pmax=1"}})
		ok(t, "observe con=1")(r, err)
		for i := 0; i < 5; i++ {
			e.ev.wait(t, 10*time.Second, "CON notification", isNotification(ob))
		}
		ok(t, "cancel")(e.srv.CancelObservation(e.ctx, ob, true))
	})

	t.Run("composite", func(t *testing.T) {
		fresh(t)
		ob, r, err := e.srv.ObserveComposite(e.ctx, ep, []lwm2m.Path{res, p("/3/0/0")}, server.CompositeOptions{})
		ok(t, "observe-composite")(r, err)
		if !hasNode(r.Nodes, "/3/0/0") || !hasNode(r.Nodes, "/33605/1/12") {
			t.Fatalf("observe-composite initial: %s", lwm2m.FormatNodes(r.Nodes))
		}
		ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(77))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
		n := waitValue(t, e, ob, "/33605/1/12", "77")
		if !hasNode(n.Response.Nodes, "/3/0/0") {
			t.Errorf("composite notification lacks /3/0/0: %s", lwm2m.FormatNodes(n.Response.Nodes))
		}
		ok(t, "cancel composite")(e.srv.CancelObservation(e.ctx, ob, true))
		drain(e, ob)
		ok(t, "write")(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.Integer(78))}, server.WriteOptions{Format: cf(lwm2m.FormatText)}))
		e.ev.none(t, 3*time.Second, "composite notification after cancel", isNotification(ob))
	})
}

func drain(e *env, ob *server.Observation) {
	time.Sleep(500 * time.Millisecond)
	for e.ev.take(isNotification(ob)) != nil {
	}
}

func TestAnjayComposite(t *testing.T) {
	e := newEnv(t)
	ep := "aj-comp"
	anjayDM(t, e, ep, uri("coap", e.udp))
	paths := []lwm2m.Path{p("/3/0/0"), p("/33605/1/12"), p("/33605/1/15")}

	formats := []struct{ body, accept lwm2m.ContentFormat }{
		{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLCBOR},
		{lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLJSON},
		{lwm2m.FormatSenMLETCHCBOR, lwm2m.FormatLwM2MCBOR},
		{lwm2m.FormatSenMLETCHJSON, lwm2m.FormatSenMLJSON},
	}
	for _, f := range formats {
		r := ok(t, "read-composite "+f.body.String())(e.srv.ReadComposite(e.ctx, ep, paths,
			server.CompositeOptions{Format: cf(f.body), Accept: cf(f.accept)}))
		if r.ContentFormat != f.accept {
			t.Errorf("read-composite %v: CF %v", f.body, r.ContentFormat)
		}
		if plain(value(t, r.Nodes, "/33605/1/12")) != "5" || plain(value(t, r.Nodes, "/33605/1/15")) != "init" ||
			!hasNode(r.Nodes, "/3/0/0") {
			t.Errorf("read-composite %v: %s", f.body, lwm2m.FormatNodes(r.Nodes))
		}
	}
	for i, f := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR} {
		n := int64(300 + i)
		s := "composite-" + strconv.Itoa(i)
		ok(t, "write-composite "+f.String())(e.srv.WriteComposite(e.ctx, ep, []lwm2m.Node{
			lwm2m.ValueNode(p("/33605/1/12"), lwm2m.Integer(n)), lwm2m.ValueNode(p("/33605/1/15"), lwm2m.String(s))}, cf(f)))
		if got := readText(t, e, ep, "/33605/1/12"); got != strconv.FormatInt(n, 10) {
			t.Errorf("after write-composite %v: /33605/1/12 = %s", f, got)
		}
		if got := readText(t, e, ep, "/33605/1/15"); got != s {
			t.Errorf("after write-composite %v: /33605/1/15 = %s", f, got)
		}
	}
	// Atomicity: one read-only target fails the whole Write-Composite.
	r, err := e.srv.WriteComposite(e.ctx, ep, []lwm2m.Node{
		lwm2m.ValueNode(p("/33605/1/12"), lwm2m.Integer(999)), lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("x"))}, nil)
	if err != nil || r.Success() {
		t.Errorf("write-composite with a read-only target: %v %v", r, err)
	}
}

func TestAnjaySend(t *testing.T) {
	e := newEnv(t)
	ep := "aj-send"
	c := anjayDM(t, e, ep, uri("coap", e.udp))
	c.send("send 1 /3/0/0 /33605/1/12")
	s := e.ev.wait(t, evTimeout, "Send", isSend(ep)).(server.SendReceived)
	if plain(value(t, s.Nodes, "/33605/1/12")) != "5" || !hasNode(s.Nodes, "/3/0/0") {
		t.Errorf("send: %s", lwm2m.FormatNodes(s.Nodes))
	}
	for _, n := range s.Nodes {
		if !n.HasTime {
			t.Errorf("send record %s without a timestamp", n.Path)
		}
	}
}

func TestAnjaySecurity(t *testing.T) {
	t.Run("psk", func(t *testing.T) {
		e := newEnv(t)
		ep, key := "aj-psk", "anjay-psk-key-0123"
		e.putPSK(ep, "aj-psk-id", []byte(key))
		startAnjay(t, ep, uri("coaps", e.dtls), "-s", "psk", "--identity-as-string", "aj-psk-id", "--key-as-string", key)
		reg := e.registered(ep)
		if reg.Identity.Mode != server.ModePSK || reg.Identity.PSKIdentity != "aj-psk-id" {
			t.Fatalf("identity %+v", reg.Identity)
		}
		if v := readText(t, e, ep, "/3/0/0"); v == "" {
			t.Error("empty /3/0/0 over DTLS")
		}
	})
	t.Run("psk-ccm8-only", func(t *testing.T) {
		// T68: TLS_PSK_WITH_AES_128_CCM_8 (0xC0A8), RFC 7925's mandatory suite.
		e := newEnv(t)
		ep, key := "aj-ccm8", "anjay-psk-key-0123"
		e.putPSK(ep, "aj-ccm8-id", []byte(key))
		startAnjay(t, ep, uri("coaps", e.dtls), "-s", "psk", "--identity-as-string", "aj-ccm8-id", "--key-as-string", key,
			"--ciphersuites", "0xC0A8")
		e.registered(ep)
	})
	t.Run("cid-nat-rebinding", func(t *testing.T) {
		// RFC 9146: with a Connection ID the session survives a new source
		// address with no new handshake and no re-Register.
		e := newEnv(t)
		ep, key := "aj-cid", "anjay-psk-key-0123"
		e.putPSK(ep, "aj-cid-id", []byte(key))
		px := newNATProxy(t, e.dtls)
		c := startAnjay(t, ep, uri("coaps", px.port()), "-s", "psk", "--identity-as-string", "aj-cid-id",
			"--key-as-string", key, "--use-connection-id")
		reg := e.registered(ep)
		// Rebind only once the 2.01 made it back through the old mapping.
		if !c.waitOutput("registration successful", evTimeout) {
			t.Fatal("client never saw the Register response")
		}
		before := px.backPort()
		px.rebind(t)
		if px.backPort() == before {
			t.Fatal("rebind kept the port")
		}
		// Uplink from the new address: an Update routed by CID.
		c.send("send-update")
		up := e.ev.wait(t, evTimeout, "Update after rebinding", isUpdated(ep)).(server.Updated)
		if up.Registration.ID != reg.ID || port(up.Registration.Addr) != px.backPort() {
			t.Errorf("update: id %s addr %v, want id %s port %d", up.Registration.ID, up.Registration.Addr, reg.ID, px.backPort())
		}
		// Downlink goes to the new address.
		if v := readText(t, e, ep, "/3/0/0"); v == "" {
			t.Error("empty read after rebinding")
		}
		if ev := e.ev.take(isRegistered(ep)); ev != nil {
			t.Errorf("client re-registered after rebinding: %+v", ev)
		}
	})
	t.Run("x509", func(t *testing.T) {
		sc := newServerCert(t)
		e := newEnv(t, withX509(sc))
		ep := "aj-x509"
		dir := t.TempDir()
		certFile, keyFile := sc.clientCert(t, dir, ep)
		srvFile := filepath.Join(dir, "server.der")
		write(t, srvFile, sc.leafDER)
		if err := e.sec.Put(server.SecurityInfo{Endpoint: ep, X509: true}); err != nil {
			t.Fatal(err)
		}
		startAnjay(t, ep, uri("coaps", e.dtls), "-s", "cert", "-C", certFile, "-K", keyFile, "-P", srvFile)
		reg := e.registered(ep)
		if reg.Identity.Mode != server.ModeX509 || reg.Identity.CertCN != ep {
			t.Fatalf("identity %+v", reg.Identity)
		}
	})
}

func TestAnjayTCP(t *testing.T) {
	e := newEnv(t)
	ep := "aj-tcp"
	c := startAnjay(t, ep, uri("coap+tcp", e.tcp))
	reg := e.registered(ep)
	if !strings.Contains(reg.Binding, "T") {
		t.Errorf("binding %q, want T", reg.Binding)
	}
	r := ok(t, "read over tcp")(e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{Accept: cf(lwm2m.FormatSenMLCBOR)}))
	if !hasNode(r.Nodes, "/3/0/0") {
		t.Errorf("read /3/0 over TCP: %s", lwm2m.FormatNodes(r.Nodes))
	}
	// Observe over TCP: notifications carry the observe token, no CON/NON.
	ok(t, "write-attributes")(e.srv.WriteAttributes(e.ctx, ep, p("/3/0/13"), []string{"pmax=1"}))
	ob, r, err := e.srv.Observe(e.ctx, ep, p("/3/0/13"), server.ObserveOptions{})
	ok(t, "observe over tcp")(r, err)
	e.ev.wait(t, evTimeout, "notification over TCP", isNotification(ob))
	// Large Block-wise-free read: 1.2 TCP carries a 4000-byte buffer at once.
	c.send("send-update")
	e.ev.wait(t, evTimeout, "Update over TCP", isUpdated(ep))
	c.closeStdin() // the demo de-registers on stdin EOF; SIGINT kills it
	e.ev.wait(t, evTimeout, "De-register over TCP", isDeregistered(ep))
}

func TestAnjayQueueMode(t *testing.T) {
	// MAX_TRANSMIT_WAIT = ACK_TIMEOUT*(2^(MAX_RETRANSMIT+1)-1)*ACK_RANDOM_FACTOR = 1*3*1 s:
	// the client closes its socket 3 s after its last exchange.
	e := newEnv(t, withConfig(func(c *server.Config) { c.QueueAwake = 3 * time.Second }))
	ep := "aj-queue"
	c := startAnjay(t, ep, uri("coap", e.udp), "-q", "UQ", "--ack-timeout", "1", "--ack-random-factor", "1", "--max-retransmit", "1")
	reg := e.registered(ep)
	if !reg.QueueMode {
		t.Fatalf("registration not in queue mode: b=%q", reg.Binding)
	}
	time.Sleep(5 * time.Second)
	if e.srv.Awake(ep) {
		t.Fatal("server still considers the client awake")
	}
	type res struct {
		r   *server.Response
		err error
		at  time.Time
	}
	done := make(chan res, 1)
	go func() {
		r, err := e.srv.Read(e.ctx, ep, p("/3/0/0"), server.ReadOptions{Accept: cf(lwm2m.FormatText)})
		done <- res{r, err, time.Now()}
	}()
	select {
	case r := <-done:
		t.Fatalf("read to a sleeping client was sent at once: %+v", r)
	case <-time.After(2 * time.Second):
	}
	wake := time.Now()
	// The client wakes with a new socket: Anjay re-registers on NoSec instead
	// of sending the Update (T24), and that registration is in queue mode too.
	c.send("send-update")
	ev := e.ev.wait(t, evTimeout, "Update or re-Register", func(ev server.Event) bool {
		return isUpdated(ep)(ev) || isRegistered(ep)(ev)
	})
	t.Logf("woke with %T", ev)
	select {
	case r := <-done:
		ok(t, "queued read")(r.r, r.err)
		if r.at.Before(wake) {
			t.Errorf("queued read finished before the client woke")
		}
	case <-time.After(evTimeout):
		t.Fatal("queued read never delivered")
	}
}

func TestAnjayBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name         string
		secID        uint16 // DM Security instance in the config
		autoID, pack bool   // AutoIDForSecurityObject; Pack expected to be served
		refuse       bool
	}{
		// Anjay keeps its BS account at /0/1: a Pack must use another ID.
		{name: "bootstrap-pack", secID: 5, pack: true},
		// AutoID cannot renumber a Pack without acc (Anjay sends none): the
		// Pack is refused (4.05) and Anjay falls back to Bootstrap-Request.
		{name: "pack-refused-autoid", secID: 1, autoID: true},
		{name: "bootstrap-request", secID: 1, autoID: true, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			b := newBS(t)
			ep := "aj-" + tc.name
			ssid := uint16(1)
			if err := b.configs.Put(ep, &bootstrap.BootstrapConfig{
				ToDelete:                []string{"/0", "/1"},
				AutoIDForSecurityObject: tc.autoID,
				Security: map[uint16]bootstrap.SecurityConfig{tc.secID: {URI: uri("coap", e.udp),
					SecurityMode: bootstrap.ModeNoSec, ServerID: &ssid}},
				Servers:    map[uint16]bootstrap.ServerConfig{1: {ShortID: ssid, Lifetime: 50, Binding: "U"}},
				RefusePack: tc.refuse,
			}); err != nil {
				t.Fatal(err)
			}
			startAnjay(t, ep, uri("coap", b.udp), "-b")
			res := b.result(t)
			if !tc.pack { // the refused Pack is reported, then the classic session
				if !res.Pack || !errors.Is(res.Err, bootstrap.ErrPackRefused) {
					t.Fatalf("pack request: pack %v err %v", res.Pack, res.Err)
				}
				res = b.result(t)
			}
			if res.Err != nil || res.Pack != tc.pack {
				t.Fatalf("bootstrap: err %v pack %v steps %+v", res.Err, res.Pack, res.Steps)
			}
			reg := e.registered(ep)
			if reg.Lifetime != 50*time.Second || reg.Version != "1.2" {
				t.Errorf("registration after bootstrap: lifetime %v version %s", reg.Lifetime, reg.Version)
			}
			// The BS account survived: Server-Initiated Bootstrap (/1/x/9)
			// works, and the client bootstraps again.
			srv1, _ := reg.Object(1)
			ok(t, "bootstrap trigger")(bootstrap.TriggerBootstrap(e.ctx, e.srv, ep, srv1.Instances[0]))
			if res = b.result(t); res.Err != nil && !errors.Is(res.Err, bootstrap.ErrPackRefused) {
				t.Fatalf("second bootstrap: %v", res.Err)
			}
		})
	}
}

// anjayFirmware is a package the demo accepts: its header ("ANJAY_FW",
// header version 2, a forced result, CRC-32 of the payload, version "1.0")
// followed by the payload. Forced result 5 makes perform_upgrade set
// Update Result Success instead of exec'ing the image.
func anjayFirmware(payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("ANJAY_FW")
	_ = binary.Write(&b, binary.BigEndian, uint16(2))
	_ = binary.Write(&b, binary.BigEndian, uint16(5))
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(payload))
	b.WriteByte(3)
	b.WriteString("1.0")
	b.Write(payload)
	return b.Bytes()
}

func TestAnjayFirmware(t *testing.T) {
	pkg := anjayFirmware(bytes.Repeat([]byte("anjay firmware image "), 400)) // ~8 KiB: Block1/Block2
	for _, m := range []fota.Method{fota.Push, fota.Pull} {
		t.Run(m.String(), func(t *testing.T) {
			var mgr *fota.Manager
			e := newEnv(t, withConfig(func(c *server.Config) {
				next := c.OnEvent
				c.OnEvent = func(ev server.Event) {
					next(ev)
					if mgr != nil {
						mgr.HandleEvent(ev)
					}
				}
			}))
			mgr = fota.New(e.srv)
			ep := "aj-fw-" + m.String()
			c := startAnjay(t, ep, uri("coap", e.udp))
			e.registered(ep)
			job := fota.Job{Method: m, Poll: 2 * time.Second}
			if m == fota.Push {
				job.Package = pkg
			} else {
				fs := fota.NewFileServer()
				t.Cleanup(func() { _ = fs.Close() })
				a, err := fs.ListenUDP("127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				fs.Add("/fw/anjay.bin", pkg)
				job.URIs = []string{uri("coap", port(a)) + "/fw/anjay.bin"}
			}
			ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
			defer cancel()
			out, err := mgr.Run(ctx, ep, job)
			if err != nil {
				t.Fatalf("firmware %v: %v", m, err)
			}
			if out.Result != fota.Success || out.Method != m {
				t.Errorf("outcome %+v", out)
			}
			if v := readText(t, e, ep, "/5/0/5"); v != "1" {
				t.Errorf("update result %s", v)
			}
			// The demo checked magic, version and CRC-32 of what it received
			// before it ran the upgrade: the image arrived intact.
			if !c.waitOutput("firmware downloaded successfully", 5*time.Second) || !c.waitOutput("*** FIRMWARE UPDATE:", 5*time.Second) {
				t.Error("client did not validate and apply the image")
			}
		})
	}
}

func TestAnjayServerTriggers(t *testing.T) {
	e := newEnv(t)
	ep := "aj-trig"
	startAnjay(t, ep, uri("coap", e.udp))
	reg := e.registered(ep)
	// /1/x/8 Registration Update Trigger.
	ok(t, "update trigger")(e.srv.TriggerUpdate(e.ctx, ep, ""))
	e.ev.wait(t, evTimeout, "Update after trigger", isUpdated(ep))
	// /1/x/4 Disable for /1/x/5 seconds: De-register, then Register again.
	srv1, _ := reg.Object(1)
	dt := lwm2m.NewPath(1, srv1.Instances[0], 5)
	ok(t, "disable timeout")(e.srv.Write(e.ctx, ep, dt, []lwm2m.Node{lwm2m.ValueNode(dt, lwm2m.Integer(3))},
		server.WriteOptions{Format: cf(lwm2m.FormatText)}))
	t0 := time.Now()
	ok(t, "disable")(e.srv.Disable(e.ctx, ep))
	e.ev.wait(t, evTimeout, "De-register after Disable", isDeregistered(ep))
	again := e.registered(ep)
	if d := time.Since(t0); d < 2*time.Second {
		t.Errorf("re-registered %v after Disable, want >= 3 s", d)
	}
	if again.ID == reg.ID {
		t.Error("same registration ID after Disable")
	}
}

// Bootstrap over DTLS PSK provisioning a PSK DM account, then
// Server-Initiated Bootstrap (/1/x/9) from the DM server.
func TestAnjayBootstrapPSK(t *testing.T) {
	e := newEnv(t)
	b := newBS(t)
	ep := "aj-bs-psk"
	bsKey, dmKey := "bootstrap-psk-key-01", "dm-psk-key-0123456"
	if err := b.sec.Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: "bs-" + ep, PSKKey: []byte(bsKey)}); err != nil {
		t.Fatal(err)
	}
	e.putPSK(ep, "dm-"+ep, []byte(dmKey))
	ssid := uint16(1)
	if err := b.configs.Put(ep, &bootstrap.BootstrapConfig{
		ToDelete: []string{"/0", "/1"},
		// Never write over the client's own BS account (Anjay keeps it at /0/1).
		AutoIDForSecurityObject: true,
		Security: map[uint16]bootstrap.SecurityConfig{1: {URI: uri("coaps", e.dtls), SecurityMode: bootstrap.ModePSK,
			PublicKeyOrID: bootstrap.Bytes("dm-" + ep), SecretKey: bootstrap.Bytes(dmKey), ServerID: &ssid}},
		Servers: map[uint16]bootstrap.ServerConfig{1: {ShortID: ssid, Lifetime: 60, Binding: "U"}},
	}); err != nil {
		t.Fatal(err)
	}
	startAnjay(t, ep, uri("coaps", b.dtls), "-b", "-s", "psk", "--identity-as-string", "bs-"+ep, "--key-as-string", bsKey)
	res := b.session(t)
	if res.Err != nil || res.Identity.PSKIdentity != "bs-"+ep {
		t.Fatalf("bootstrap: err %v identity %+v", res.Err, res.Identity)
	}
	reg := e.registered(ep)
	if reg.Identity.PSKIdentity != "dm-"+ep {
		t.Fatalf("DM identity %+v", reg.Identity)
	}
	// Server-Initiated Bootstrap: the DM server triggers /1/x/9.
	srv1, _ := reg.Object(1)
	ok(t, "bootstrap trigger")(bootstrap.TriggerBootstrap(e.ctx, e.srv, ep, srv1.Instances[0]))
	res = b.session(t)
	if res.Err != nil {
		t.Fatalf("second bootstrap: %v", res.Err)
	}
	e.registered(ep)
}
