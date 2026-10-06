//go:build interop

package peers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Anjay Lite's integration test_app, driven over its JSON-RPC socket
// (frame: 0xF7, big-endian u32 length, {"name","args"} JSON). LwM2M 1.2,
// test object 1234 with instances 0-2 (res 0 double R = iid, 1 string R,
// 2 int RWM, 3 double W, 4 string RW of at most 10 bytes), Device /3/0,
// a FOTA mock /5/0 whose package is Adler-32 (BE) + data.

type lite struct {
	t    *testing.T
	proc *proc
	conn net.Conn
}

func startLite(t *testing.T) *lite {
	t.Helper()
	path := bin(t, "ANJAY_LITE_APP")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	l := &lite{t: t, proc: start(t, path, strconv.Itoa(port(ln.Addr())))}
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	if l.conn, err = ln.Accept(); err != nil {
		t.Fatalf("test_app did not connect: %v", err)
	}
	t.Cleanup(func() { _ = l.conn.Close() })
	return l
}

func (l *lite) call(name string, args ...any) json.RawMessage {
	l.t.Helper()
	if args == nil {
		args = []any{}
	}
	body, _ := json.Marshal(map[string]any{"name": name, "args": args})
	hdr := []byte{0xF7, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(body)))
	_ = l.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := l.conn.Write(append(hdr, body...)); err != nil {
		l.t.Fatalf("rpc %s: %v", name, err)
	}
	if _, err := io.ReadFull(l.conn, hdr); err != nil {
		l.t.Fatalf("rpc %s: %v", name, err)
	}
	resp := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
	if _, err := io.ReadFull(l.conn, resp); err != nil {
		l.t.Fatalf("rpc %s: %v", name, err)
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil || out.Error != "" {
		l.t.Fatalf("rpc %s: %s %v", name, resp, err)
	}
	return out.Result
}

// callOK calls an int-returning command and requires 0.
func (l *lite) callOK(name string, args ...any) {
	l.t.Helper()
	if r := string(l.call(name, args...)); r != "0" {
		l.t.Fatalf("rpc %s returned %s", name, r)
	}
}

type liteServer struct {
	Bootstrap bool              `json:"bootstrap"`
	URI       string            `json:"uri"`
	Security  map[string]string `json:"security"`
	Lifetime  int               `json:"lifetime,omitempty"`
}

func nosec(uri string) liteServer {
	return liteServer{URI: uri, Security: map[string]string{"kind": "nosec"}, Lifetime: 60}
}

func liteDM(t *testing.T, e *env, ep string) *lite {
	t.Helper()
	l := startLite(t)
	l.callOK("init", map[string]any{"endpoint": ep, "servers": []liteServer{nosec(uri("coap", e.udp))}})
	return l
}

func TestAnjayLiteRegistration(t *testing.T) {
	e := newEnv(t)
	ep := "al-reg"
	l := liteDM(t, e, ep)
	reg := e.registered(ep)
	// T1 query order ep,lt,lwm2m,b; T4 b=U always; T11 no </> root link.
	if reg.Version != "1.2" || reg.Binding != "U" || reg.Lifetime != 60*time.Second {
		t.Errorf("version %q binding %q lifetime %v", reg.Version, reg.Binding, reg.Lifetime)
	}
	for _, o := range []uint16{1, 3, 1234} {
		if !reg.HasObject(o) {
			t.Errorf("object list %q lacks /%d", reg.RawLinks, o)
		}
	}
	// T21: Location-Path at most 2 segments of 40 bytes; ours is rd/<id>.
	if len(reg.ID) > 40 {
		t.Errorf("registration ID %q too long for Anjay Lite", reg.ID)
	}
	l.call("send_update")
	up := e.ev.wait(t, evTimeout, "Update", isUpdated(ep)).(server.Updated)
	if up.Registration.ID != reg.ID {
		t.Errorf("update moved the registration")
	}
	// test_app shutdown only closes the socket; disabling the server (the
	// /1/x/4 path) de-registers.
	l.callOK("disable_server", 60)
	d := e.ev.wait(t, evTimeout, "De-register", isDeregistered(ep)).(server.Deregistered)
	if d.Reason != server.ReasonDeregistered {
		t.Errorf("reason %v", d.Reason)
	}
}

func TestAnjayLiteDeviceManagement(t *testing.T) {
	e := newEnv(t)
	ep := "al-dm"
	liteDM(t, e, ep)
	e.registered(ep)

	// T34: no Accept gives LwM2M CBOR even for one resource; it decodes.
	r := ok(t, "read /3/0/0")(e.srv.Read(e.ctx, ep, p("/3/0/0"), server.ReadOptions{}))
	if len(r.Nodes) != 1 || r.DecodeErr != nil {
		t.Fatalf("read /3/0/0 without Accept: CF %v %s %v", r.ContentFormat, lwm2m.FormatNodes(r.Nodes), r.DecodeErr)
	}
	manu := plain(r.Nodes[0].Value)
	var ref []lwm2m.Node
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatLwM2MCBOR, lwm2m.FormatSenMLCBOR} {
		r := ok(t, "read /1234/1 "+f.String())(e.srv.Read(e.ctx, ep, p("/1234/1"), server.ReadOptions{Accept: cf(f)}))
		if r.ContentFormat != f || r.DecodeErr != nil {
			t.Fatalf("read %v: CF %v %v", f, r.ContentFormat, r.DecodeErr)
		}
		got := pick(r.Nodes, "/1234/1/0", "/1234/1/1")
		if len(got) != 2 {
			t.Fatalf("read /1234/1 %v: %s", f, lwm2m.FormatNodes(r.Nodes))
		}
		if ref == nil {
			ref = got
		} else if !sameNodes(ref, got) {
			t.Errorf("%v disagrees: %s vs %s", f, lwm2m.FormatNodes(got), lwm2m.FormatNodes(ref))
		}
	}
	if v := value(t, ref, "/1234/1/0"); v.Float != 1 {
		t.Errorf("/1234/1/0 = %v, want 1.0", v)
	}
	r = ok(t, "read text")(e.srv.Read(e.ctx, ep, p("/3/0/0"), server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
	if string(r.Payload) != manu {
		t.Errorf("text /3/0/0 %q vs %q", r.Payload, manu)
	}
	// T35: an unsupported Accept is 4.15 on Lite (no SenML JSON).
	r, err := e.srv.Read(e.ctx, ep, p("/3/0"), server.ReadOptions{Accept: cf(lwm2m.FormatSenMLJSON)})
	if err != nil || r.Success() || (r.Code != codes.UnsupportedMediaType && r.Code != codes.NotAcceptable) {
		t.Errorf("read with SenML JSON: %v %v", r, err)
	}

	// Writes in each format Lite decodes (TLV decode-only included).
	res := p("/1234/0/4")
	for i, f := range []lwm2m.ContentFormat{lwm2m.FormatText, lwm2m.FormatCBOR, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR, lwm2m.FormatTLV} {
		want := "v" + strconv.Itoa(i)
		ok(t, "write "+f.String())(e.srv.Write(e.ctx, ep, res, []lwm2m.Node{lwm2m.ValueNode(res, lwm2m.String(want))}, server.WriteOptions{Format: cf(f)}))
		r := ok(t, "read back")(e.srv.Read(e.ctx, ep, res, server.ReadOptions{Accept: cf(lwm2m.FormatText)}))
		if string(r.Payload) != want {
			t.Errorf("after %v write: %q, want %s", f, r.Payload, want)
		}
	}
	// Multi-instance resource replace, then a partial update of one instance.
	ok(t, "write multi")(e.srv.Write(e.ctx, ep, p("/1234/0/2"), []lwm2m.Node{
		lwm2m.ValueNode(p("/1234/0/2/0"), lwm2m.Integer(10)), lwm2m.ValueNode(p("/1234/0/2/1"), lwm2m.Integer(11))},
		server.WriteOptions{Format: cf(lwm2m.FormatSenMLCBOR)}))
	ok(t, "partial")(e.srv.Write(e.ctx, ep, p("/1234/0"), []lwm2m.Node{lwm2m.ValueNode(p("/1234/0/2/1"), lwm2m.Integer(12))},
		server.WriteOptions{Mode: server.PartialUpdate, Format: cf(lwm2m.FormatLwM2MCBOR)}))
	r = ok(t, "read multi")(e.srv.Read(e.ctx, ep, p("/1234/0/2"), server.ReadOptions{Accept: cf(lwm2m.FormatSenMLCBOR)}))
	if plain(value(t, r.Nodes, "/1234/0/2/0")) != "10" || plain(value(t, r.Nodes, "/1234/0/2/1")) != "12" {
		t.Errorf("multi after partial update: %s", lwm2m.FormatNodes(r.Nodes))
	}
	// Write-only resource: Read is 4.05.
	code(t, "read write-only", codes.MethodNotAllowed)(e.srv.Read(e.ctx, ep, p("/1234/0/3"), server.ReadOptions{}))

	code(t, "create", codes.Created)(e.srv.Create(e.ctx, ep, p("/1234"), []lwm2m.Node{
		lwm2m.ValueNode(p("/1234/5/4"), lwm2m.String("made"))}, cf(lwm2m.FormatSenMLCBOR)))
	if v := readText(t, e, ep, "/1234/5/4"); v != "made" {
		t.Errorf("created /1234/5/4 = %q", v)
	}
	code(t, "delete", codes.Deleted)(e.srv.Delete(e.ctx, ep, p("/1234/5")))
	code(t, "read deleted", codes.NotFound)(e.srv.Read(e.ctx, ep, p("/1234/5"), server.ReadOptions{Accept: cf(lwm2m.FormatSenMLCBOR)}))

	paths := discover(t, e, ep, "/1234", nil)
	if !paths["/1234/0"] {
		t.Errorf("discover /1234: %v", paths)
	}
	one := 1
	if paths := discover(t, e, ep, "/1234", &one); !paths["/1234/0"] || paths["/1234/0/4"] {
		t.Errorf("discover /1234 depth=1: %v", paths)
	}

	// Composite (Read ON, ETCH-CBOR request bodies).
	for _, body := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLETCHCBOR} {
		r := ok(t, "read-composite "+body.String())(e.srv.ReadComposite(e.ctx, ep, []lwm2m.Path{p("/3/0/0"), p("/1234/2/0")},
			server.CompositeOptions{Format: cf(body), Accept: cf(lwm2m.FormatSenMLCBOR)}))
		if plain(value(t, r.Nodes, "/3/0/0")) != manu || value(t, r.Nodes, "/1234/2/0").Float != 2 {
			t.Errorf("read-composite %v: %s", body, lwm2m.FormatNodes(r.Nodes))
		}
	}
	ok(t, "write-composite")(e.srv.WriteComposite(e.ctx, ep, []lwm2m.Node{
		lwm2m.ValueNode(p("/1234/0/4"), lwm2m.String("c0")), lwm2m.ValueNode(p("/1234/1/4"), lwm2m.String("c1"))}, cf(lwm2m.FormatSenMLCBOR)))
	if readText(t, e, ep, "/1234/0/4") != "c0" || readText(t, e, ep, "/1234/1/4") != "c1" {
		t.Error("write-composite not applied")
	}
}

func TestAnjayLiteObserve(t *testing.T) {
	e := newEnv(t)
	ep, res := "al-obs", p("/1234/0/0")
	l := liteDM(t, e, ep)
	e.registered(ep)
	ok(t, "write-attributes")(e.srv.WriteAttributes(e.ctx, ep, res, []string{"pmin=0", "pmax=30"}))
	ob, r, err := e.srv.Observe(e.ctx, ep, res, server.ObserveOptions{Accept: cf(lwm2m.FormatSenMLCBOR)})
	ok(t, "observe")(r, err)
	if v := value(t, r.Nodes, "/1234/0/0"); v.Float != 0 {
		t.Errorf("initial %v", v)
	}
	l.callOK("set_test_value", 0, 3.5)
	waitValue(t, e, ob, "/1234/0/0", "3.5")
	ok(t, "cancel")(e.srv.CancelObservation(e.ctx, ob, true))
	drain(e, ob)
	l.callOK("set_test_value", 0, 4.5)
	e.ev.none(t, 3*time.Second, "notification after cancel", isNotification(ob))

	// 1.2 attributes in the Observe request.
	ob, r, err = e.srv.Observe(e.ctx, ep, p("/1234/1/0"), server.ObserveOptions{Query: []string{"gt=10"}})
	ok(t, "observe gt")(r, err)
	l.callOK("set_test_value", 1, 5)
	l.callOK("set_test_value", 1, 20)
	waitValue(t, e, ob, "/1234/1/0", "20")
}

func TestAnjayLiteSend(t *testing.T) {
	e := newEnv(t)
	ep := "al-send"
	l := liteDM(t, e, ep)
	e.registered(ep)
	for _, f := range []string{"senml_cbor", "lwm2m_cbor"} {
		l.callOK("send", map[string]any{"content_format": f, "resources": []map[string]string{
			{"path": "/3/0/0", "type": "string"}, {"path": "/1234/2/0", "type": "double"}}})
		s := e.ev.wait(t, evTimeout, "Send "+f, isSend(ep)).(server.SendReceived)
		if value(t, s.Nodes, "/1234/2/0").Float != 2 || !hasNode(s.Nodes, "/3/0/0") {
			t.Errorf("send %s: %s", f, lwm2m.FormatNodes(s.Nodes))
		}
	}
}

func TestAnjayLitePSK(t *testing.T) {
	e := newEnv(t)
	ep, key := "al-psk", "anjay-lite-psk-key"
	e.putPSK(ep, "al-psk-id", []byte(key))
	l := startLite(t)
	l.callOK("init", map[string]any{"endpoint": ep, "servers": []liteServer{{URI: uri("coaps", e.dtls), Lifetime: 60,
		Security: map[string]string{"kind": "psk", "psk_identity": "al-psk-id", "psk_key": key}}}})
	reg := e.registered(ep)
	if reg.Identity.Mode != server.ModePSK || reg.Identity.PSKIdentity != "al-psk-id" {
		t.Fatalf("identity %+v", reg.Identity)
	}
	if v := readText(t, e, ep, "/1234/0/1"); v == "" {
		t.Error("empty read over DTLS")
	}
}

func TestAnjayLiteBootstrap(t *testing.T) {
	e := newEnv(t)
	b := newBS(t)
	ep := "al-bs"
	ssid := uint16(1)
	if err := b.configs.Put(ep, &bootstrap.BootstrapConfig{
		ToDelete: []string{"/0", "/1"},
		// Never write over the client's own BS account (Anjay keeps it at /0/1).
		AutoIDForSecurityObject: true,
		Security:                map[uint16]bootstrap.SecurityConfig{1: {URI: uri("coap", e.udp), SecurityMode: bootstrap.ModeNoSec, ServerID: &ssid}},
		Servers:                 map[uint16]bootstrap.ServerConfig{1: {ShortID: ssid, Lifetime: 55, Binding: "U"}},
	}); err != nil {
		t.Fatal(err)
	}
	l := startLite(t)
	l.callOK("init", map[string]any{"endpoint": ep, "servers": []liteServer{{Bootstrap: true, URI: uri("coap", b.udp),
		Security: map[string]string{"kind": "nosec"}}}})
	res := b.result(t)
	if res.Err != nil {
		t.Fatalf("bootstrap: %v steps %+v", res.Err, res.Steps)
	}
	if res.Format != lwm2m.FormatSenMLCBOR {
		t.Errorf("bootstrap writes in %v, want the client's pct 112", res.Format)
	}
	if reg := e.registered(ep); reg.Lifetime != 55*time.Second {
		t.Errorf("lifetime %v, want the bootstrapped 55s", reg.Lifetime)
	}
}

func TestAnjayLiteQueueMode(t *testing.T) {
	e := newEnv(t, withConfig(func(c *server.Config) { c.QueueAwake = 2 * time.Second }))
	ep := "al-queue"
	l := startLite(t)
	l.callOK("init", map[string]any{"endpoint": ep, "servers": []liteServer{nosec(uri("coap", e.udp))},
		"queue_mode": true, "queue_mode_timeout_s": 2})
	reg := e.registered(ep)
	if !reg.QueueMode {
		t.Fatalf("not in queue mode: %q", reg.Binding)
	}
	time.Sleep(4 * time.Second)
	done := make(chan error, 1)
	go func() {
		r, err := e.srv.Read(e.ctx, ep, p("/1234/0/1"), server.ReadOptions{Accept: cf(lwm2m.FormatText)})
		if err == nil && !r.Success() {
			err = fmt.Errorf("%s", server.CodeString(r.Code))
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("read to a sleeping client finished at once: %v", err)
	case <-time.After(2 * time.Second):
	}
	l.call("send_update")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued read: %v", err)
		}
	case <-time.After(evTimeout):
		t.Fatal("queued read never delivered")
	}
}

// litePackage is the FOTA mock's package: Adler-32 (big-endian) of data, then data.
func litePackage(data []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, adler32.Checksum(data)), data...)
}

func TestAnjayLiteFirmware(t *testing.T) {
	pkg := litePackage(bytes.Repeat([]byte("anjay lite image "), 300)) // ~5 KiB, block-wise
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
			ep := "al-fw-" + m.String()
			l := startLite(t)
			l.callOK("init", map[string]any{"endpoint": ep, "servers": []liteServer{nosec(uri("coap", e.udp))},
				"fota_config": map[string]any{"reboot_required": false}})
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
				fs.Add("/fw/lite.bin", pkg)
				job.URIs = []string{uri("coap", port(a)) + "/fw/lite.bin"}
			}
			ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
			defer cancel()
			out, err := mgr.Run(ctx, ep, job)
			if err != nil {
				t.Fatalf("firmware %v: %v", m, err)
			}
			if out.Result != fota.Success {
				t.Errorf("outcome %+v", out)
			}
		})
	}
}
