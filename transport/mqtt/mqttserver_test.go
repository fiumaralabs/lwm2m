package mqtt

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

var testSession = MQTTServerParams{
	WillRetain: true, WillTopic: "lwm2m/server/status", WillMessage: "offline", CleanSession: true,
	WillQoS: 1, KeepAlive: 45, ClientID: "lwm2mServer01", UserName: "srv", Password: []byte{0, 1, 2},
}

// Proves: MQTT-12
func TestMQTTServerValidate(t *testing.T) {
	if err := testSession.Validate(); err != nil {
		t.Fatal(err)
	}
	ok := func(f func(*MQTTServerParams)) error {
		p := testSession
		f(&p)
		return p.Validate()
	}
	for _, id := range []string{"a", "Z9", strings.Repeat("x", 23), "0123456789abcdefghijklm"} {
		if err := ok(func(p *MQTTServerParams) { p.ClientID = id }); err != nil {
			t.Errorf("client id %q: %v", id, err)
		}
	}
	for _, id := range []string{"", strings.Repeat("x", 24), "lwm2m-server", "a b", "é", "a/b", "a\x00"} {
		if err := ok(func(p *MQTTServerParams) { p.ClientID = id }); err == nil {
			t.Errorf("client id %q accepted", id)
		}
	}
	for q := uint8(0); q <= 3; q++ {
		if err := ok(func(p *MQTTServerParams) { p.WillQoS = q }); err != nil {
			t.Errorf("Will QoS %d: %v", q, err)
		}
	}
	for name, f := range map[string]func(*MQTTServerParams){
		"Will QoS 4":            func(p *MQTTServerParams) { p.WillQoS = 4 },
		"user name not UTF-8":   func(p *MQTTServerParams) { p.UserName = "\xff" },
		"will message no topic": func(p *MQTTServerParams) { p.WillTopic = "" },
		"password too long":     func(p *MQTTServerParams) { p.Password = make([]byte, 65536) },
	} {
		if err := ok(f); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Keep Alive is 0..65535 by type; both ends are written as-is.
	for _, ka := range []uint16{0, 65535} {
		if err := ok(func(p *MQTTServerParams) { p.KeepAlive = ka }); err != nil {
			t.Errorf("keep alive %d: %v", ka, err)
		}
	}
}

// Proves: MQTT-12, MQTT-11
func TestMQTTServerNodes(t *testing.T) {
	ns, err := testSession.Nodes(2)
	if err != nil {
		t.Fatal(err)
	}
	r := func(res uint16, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(lwm2m.NewPath(24, 2, res), v) }
	want := []lwm2m.Node{
		r(0, lwm2m.Boolean(true)), r(1, lwm2m.String("lwm2m/server/status")), r(2, lwm2m.String("offline")),
		r(3, lwm2m.Boolean(true)), r(4, lwm2m.Unsigned(1)), r(5, lwm2m.Unsigned(45)),
		r(6, lwm2m.String("lwm2mServer01")), r(7, lwm2m.String("srv")), r(8, lwm2m.Opaque([]byte{0, 1, 2})),
	}
	if !lwm2m.NodesEqual(ns, want) {
		t.Fatalf("nodes %v", ns)
	}
	s := model.Default().Schema(map[uint16]model.Version{0: {Major: 1, Minor: 2}, 23: {Major: 1, Minor: 0}, 24: {Major: 1, Minor: 0}})
	if err := s.CheckCreate(24, ns); err != nil {
		t.Errorf("/24 nodes do not fit E.11: %v", err)
	}
	min, err := MQTTServerParams{ClientID: "c"}.Nodes(0)
	if err != nil || s.CheckCreate(24, min) != nil || len(min) != 5 {
		t.Errorf("minimal /24 = %v, %v", min, err)
	}
	if _, err := (MQTTServerParams{}).Nodes(0); err == nil {
		t.Error("/24 without the mandatory Client Identifier accepted")
	}

	ws, err := BootstrapWrites(1, 2, testSession, 3, testKey)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, w := range ws {
		paths = append(paths, w.Path.String())
		for _, n := range w.Nodes {
			if !n.Path.HasPrefix(w.Path) {
				t.Errorf("%s holds %s", w.Path, n.Path)
			}
		}
		if w.Path.Object() == 0 {
			if err := s.CheckCreate(0, w.Nodes); err != nil { // BS-only resources: not DM-writable
				t.Errorf("%s: %v", w.Path, err)
			}
		}
	}
	if got := strings.Join(paths, " "); got != "/24/2 /0/1/26 /23/3 /0/1/27" {
		t.Errorf("writes %s", got)
	}
	if l := ws[1].Nodes[0].Value.Link; l != (lwm2m.ObjLink{Object: 24, Instance: 2}) {
		t.Errorf("/0/1/26 = %v", l)
	}
	if l := ws[3].Nodes[0].Value.Link; l != (lwm2m.ObjLink{Object: 23, Instance: 3}) {
		t.Errorf("/0/1/27 = %v", l)
	}
	if ws, _ := BootstrapWrites(1, 2, testSession, 3, nil); len(ws) != 2 {
		t.Errorf("without COSE: %d writes, want /24 and its link", len(ws))
	}
}

// connectCapture records the CONNECT packets the broker receives.
type connectCapture struct {
	mochi.HookBase
	mu  sync.Mutex
	got []packets.ConnectParams
}

func (*connectCapture) ID() string           { return "capture" }
func (*connectCapture) Provides(b byte) bool { return b == mochi.OnConnect }
func (c *connectCapture) OnConnect(_ *mochi.Client, pk packets.Packet) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, pk.Connect)
	return nil
}

// Proves: MQTT-12
func TestSessionFromMQTTServer(t *testing.T) {
	capt := new(connectCapture)
	addr := startBroker(t, nil, capt)
	srv := server.New(server.Config{RequestTimeout: time.Second})
	t.Cleanup(func() { _ = srv.Close() })
	b, err := New(srv, Config{Broker: "mqtt://" + addr, ClientID: "ignored", Session: &testSession})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	capt.mu.Lock()
	defer capt.mu.Unlock()
	if len(capt.got) != 1 {
		t.Fatalf("%d CONNECTs", len(capt.got))
	}
	c := capt.got[0]
	if c.ClientIdentifier != "lwm2mServer01" || !c.Clean || c.Keepalive != 45 ||
		!c.UsernameFlag || string(c.Username) != "srv" || !c.PasswordFlag || string(c.Password) != "\x00\x01\x02" ||
		!c.WillFlag || c.WillTopic != "lwm2m/server/status" || string(c.WillPayload) != "offline" || c.WillQos != 1 || !c.WillRetain {
		t.Fatalf("CONNECT %+v", c)
	}

	bad := testSession
	bad.ClientID = "has-dash"
	if _, err := New(srv, Config{Broker: "mqtt://" + addr, Session: &bad}); err == nil {
		t.Error("invalid /24 session accepted")
	}
	bad = testSession
	bad.WillQoS = 3 // valid per E.11 but not an MQTT 3.1.1 QoS
	if _, err := New(srv, Config{Broker: "mqtt://" + addr, Session: &bad}); err == nil {
		t.Error("Will QoS 3 sent to the broker")
	}
}
