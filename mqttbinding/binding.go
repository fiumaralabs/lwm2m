// Package mqttbinding is the LwM2M-over-MQTT transport binding "M" of the
// Server (T §8): the Server is an MQTT client of a broker, subscribes to
// {PREFIX}/lwm2m/rd/# and exchanges CBOR-encoded LwM2M messages with
// clients on {PREFIX}/lwm2m/rd/{ENDPOINT} (T §8.2, §8.3).
//
// NewBootstrap serves the Bootstrap interface of a Bootstrap-Server on
// {PREFIX}/lwm2m/bs/{ENDPOINT} the same way (T §8.3.1). COSE end-to-end
// protection (T §8.6, §8.8) is per endpoint: see Config.COSE.
package mqttbinding

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Config configures the binding.
type Config struct {
	// Broker is the broker URI; the scheme MUST be mqtt or mqtts (T §8.8).
	Broker   string
	Prefix   string // topic PREFIX (tenant, server ID); "" omits it (T §8.2)
	ClientID string // MQTT client identifier; default "lwm2m-server"
	Username string
	Password string
	TLS      *tls.Config // for mqtts

	// Session, when set, supplies the MQTT CONNECT parameters as a /24
	// MQTT Server instance would (Core E.11) and overrides ClientID,
	// Username and Password.
	Session *MQTTServerParams

	// COSE returns the /23 key of an endpoint whose /0 instance for this
	// Server links a COSE instance, or nil. Such an endpoint MUST use COSE
	// (T §8.8): its messages are sealed and opened with the key, and an
	// unprotected request is refused.
	COSE func(ep string) *COSEKey
}

// qos is used for every publish and subscription. T §8 sets none;
// at-least-once is the cheapest that survives a broker reconnect.
const qos = 1

// Binding connects a Server to an MQTT broker.
type Binding struct {
	srv  *server.Server
	bs   *bootstrap.Server // set: this is the Bootstrap-Server's binding
	cfg  Config
	cli  mqtt.Client
	base string // "{PREFIX}/lwm2m/rd/" or "{PREFIX}/lwm2m/bs/"

	mu      sync.Mutex
	pending map[uint64]chan *Message
	peers   map[string]*peer
}

// New connects to the broker and starts serving the Registration, Device
// Management and Information Reporting interfaces for srv.
func New(srv *server.Server, cfg Config) (*Binding, error) {
	b := &Binding{srv: srv, cfg: cfg, pending: map[uint64]chan *Message{}, peers: map[string]*peer{}}
	if err := b.connect("rd", "lwm2m-server"); err != nil {
		return nil, err
	}
	return b, nil
}

// connect subscribes to {PREFIX}/lwm2m/{iface}/# (T §8.3).
func (b *Binding) connect(iface, defaultID string) error {
	cfg := b.cfg
	u, err := url.Parse(cfg.Broker)
	if err != nil {
		return err
	}
	if u.Scheme != "mqtt" && u.Scheme != "mqtts" {
		return fmt.Errorf("mqttbinding: broker scheme %q, want mqtt or mqtts (MQTT-01)", u.Scheme)
	}
	b.base = "lwm2m/" + iface + "/"
	if cfg.Prefix != "" {
		b.base = cfg.Prefix + "/" + b.base
	}
	id := cfg.ClientID
	if id == "" {
		id = defaultID
	}
	o := mqtt.NewClientOptions().AddBroker(cfg.Broker).SetClientID(id).
		SetUsername(cfg.Username).SetPassword(cfg.Password).SetTLSConfig(cfg.TLS).
		SetOrderMatters(false). // handlers may block on downlink exchanges
		SetAutoReconnect(true).SetConnectRetry(false)
	if cfg.Session != nil {
		if err := cfg.Session.apply(o); err != nil {
			return err
		}
	}
	o.SetOnConnectHandler(func(c mqtt.Client) {
		c.Subscribe(b.base+"#", qos, b.onMessage) // T §8.3
	})
	b.cli = mqtt.NewClient(o)
	if err := wait(b.cli.Connect()); err != nil {
		return err
	}
	// The subscription is made by the on-connect handler; make sure it is
	// in place before returning so no early request is lost.
	if err := wait(b.cli.Subscribe(b.base+"#", qos, b.onMessage)); err != nil {
		b.cli.Disconnect(0)
		return err
	}
	return nil
}

func wait(t mqtt.Token) error {
	if !t.WaitTimeout(10 * time.Second) {
		return errors.New("mqttbinding: broker timeout")
	}
	return t.Error()
}

// Close disconnects from the broker.
func (b *Binding) Close() { b.cli.Disconnect(250) }

// Topic returns the rd (or, for NewBootstrap, bs) topic of an endpoint.
func (b *Binding) Topic(ep string) string { return b.base + ep }

func (b *Binding) publish(ep string, m *Message) error {
	var data []byte
	var err error
	if k := b.key(ep); k != nil {
		data, err = seal(k, m)
	} else {
		data, err = Marshal(m)
	}
	if err != nil {
		return err
	}
	return wait(b.cli.Publish(b.Topic(ep), qos, false, data))
}

func (b *Binding) key(ep string) *COSEKey {
	if b.cfg.COSE == nil {
		return nil
	}
	return b.cfg.COSE(ep)
}

// refuse answers an unprotected request from an endpoint that must use
// COSE, in clear since the client did not protect it. Register gets 403
// (registration not allowed, T Tbl 8.5-2); the other uplink operations
// have no "forbidden" result, so 400. Responses and Notifies carry no
// request to answer and are dropped.
// On bs, Bootstrap-Request gets 400 and Bootstrap-Pack-Request 401
// (T Tbl 8.5-1); the other bs operations are the BS's own.
func (b *Binding) refuse(ep string, m *Message) {
	if m.Operation == nil {
		return
	}
	op := *m.Operation
	r := uint64(400)
	switch {
	case b.bs != nil && op == OpBootstrapRequest:
	case b.bs != nil && op == OpBootstrapPack:
		r = 401
	case b.bs != nil, op == OpNotify, op >= OpRead && op <= OpCancelObserve:
		return
	case op == OpRegister:
		r = 403
	}
	if data, err := Marshal(&Message{Token: m.Token, Result: u64(r)}); err == nil {
		_ = wait(b.cli.Publish(b.Topic(ep), qos, false, data))
	}
}

func (b *Binding) peer(ep string) *peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.peers[ep]
	if !ok {
		p = &peer{b: b, ep: ep}
		b.peers[ep] = p
	}
	return p
}

// onMessage handles everything published under {PREFIX}/lwm2m/rd/ (or bs/).
// Requests and responses share the topic, so the Server also receives its
// own publications (A-7): results that match no pending request and
// operations only a Server sends are dropped.
func (b *Binding) onMessage(_ mqtt.Client, pm mqtt.Message) {
	ep, ok := strings.CutPrefix(pm.Topic(), b.base)
	if !ok || ep == "" {
		return
	}
	m, err := unwrap(pm.Payload(), b.key(ep))
	if errors.Is(err, errUnprotected) {
		b.refuse(ep, m)
		return
	}
	if err != nil {
		return // no (authentic) token to answer with
	}
	if m.Result != nil {
		b.mu.Lock()
		ch := b.pending[m.Token]
		delete(b.pending, m.Token)
		b.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	if b.bs != nil {
		b.bootstrapUplink(ep, m)
		return
	}
	op := *m.Operation
	if op >= OpRead && op <= OpCancelObserve {
		return // our own downlink request echoed back
	}
	p := b.peer(ep)
	if op == OpNotify {
		b.notify(p, m)
		return
	}
	req, code := b.uplink(ep, m)
	var resp *server.Message
	after := func() {}
	if code != 0 {
		resp = &server.Message{Code: code}
	} else {
		resp, after = b.srv.HandleUplink(p, req)
	}
	b.respond(ep, m.Token, resp)
	after()
}

// respond publishes the result of the request with token tok.
func (b *Binding) respond(ep string, tok uint64, resp *server.Message) {
	out := &Message{Token: tok, Result: u64(Result(resp.Code)), Payload: resp.Payload}
	if resp.Format != nil {
		out.CT = u64(uint64(*resp.Format))
	}
	_ = b.publish(ep, out)
}

// uplink maps a client request to a binding-neutral Message (T §8.3.2,
// §8.3.4), or returns the error code to answer with.
func (b *Binding) uplink(ep string, m *Message) (*server.Message, codes.Code) {
	switch *m.Operation {
	case OpRegister:
		if m.EP != nil && *m.EP != ep {
			return nil, codes.BadRequest // ENDPOINT has to match the ep (T §8.2)
		}
		q := []string{"ep=" + ep}
		if m.Lifetime != nil {
			q = append(q, "lt="+strconv.FormatUint(*m.Lifetime, 10))
		}
		if m.Version != nil {
			q = append(q, "lwm2m="+*m.Version)
		}
		return &server.Message{Code: codes.POST, Path: "/rd", Query: append(q, regQuery(m)...),
			Format: linkFormat(m.Payload), Payload: m.Payload}, 0
	case OpUpdate, OpDeregister:
		reg, ok := b.srv.Store().ByEndpoint(ep)
		if !ok {
			return nil, codes.NotFound // the ENDPOINT level is the location (REG-05)
		}
		msg := &server.Message{Code: codes.DELETE, Path: "/rd/" + reg.ID}
		if *m.Operation == OpUpdate {
			var q []string
			if m.Lifetime != nil {
				q = append(q, "lt="+strconv.FormatUint(*m.Lifetime, 10))
			}
			msg = &server.Message{Code: codes.POST, Path: "/rd/" + reg.ID, Query: append(q, regQuery(m)...),
				Format: linkFormat(m.Payload), Payload: m.Payload}
		}
		return msg, 0
	case OpSend:
		return &server.Message{Code: codes.POST, Path: "/dp", Format: ctOf(m), Payload: m.Payload}, 0
	case OpBootstrapRequest, OpBootstrapWrite, OpBootstrapRead, OpBootstrapDelete, OpBootstrapDiscover, OpBootstrapFinish:
		return nil, codes.BadRequest // Bootstrap operations belong on the bs topic
	}
	return nil, codes.NotImplemented // T Tbl 8.5-5
}

func regQuery(m *Message) []string {
	var q []string
	if m.B != nil {
		q = append(q, "b="+*m.B)
	}
	if m.SMS != nil {
		q = append(q, "sms="+*m.SMS)
	}
	return q
}

func linkFormat(p []byte) *lwm2m.ContentFormat {
	if len(p) == 0 {
		return nil
	}
	f := lwm2m.FormatLinkFormat
	return &f
}

func ctOf(m *Message) *lwm2m.ContentFormat {
	if m.CT == nil {
		return nil
	}
	f := lwm2m.ContentFormat(*m.CT)
	return &f
}

// notify delivers a Notify (op 23) to its observation. A Notify for an
// unknown observation gets a Cancel-Observe, the MQTT counterpart of the
// CoAP Reset (OBS-02). A known Notify gets no LwM2M reply: its token is
// the observation's, so a 205 on the shared topic could not be told apart
// from the client's answer to a Cancel-Observe; the QoS 1 PUBACK is the
// acknowledgement.
func (b *Binding) notify(p *peer, m *Message) {
	tok := tokenBytes(m.Token)
	known := false
	for _, ob := range b.srv.Observations(p.ep) {
		if ob.ID == fmt.Sprintf("%x", tok) {
			known = true
		}
	}
	if !known {
		_ = b.publish(p.ep, &Message{Operation: u64(OpCancelObserve), Token: m.Token})
		return
	}
	_, after := b.srv.HandleUplink(p, &server.Message{Code: codes.Content, Token: tok, Format: ctOf(m), Payload: m.Payload})
	after()
}

// Result maps a CoAP-numbered code to an MQTT result: 2.05 -> 205
// (T §8.5, "loosely based on the CoAP response codes").
func Result(c codes.Code) uint64 { return uint64(c>>5)*100 + uint64(c&0x1f) }

// Code is the inverse of Result. Results with no CoAP form fall back to
// x.00 of their class.
func Code(r uint64) codes.Code {
	class, detail := r/100, r%100
	if class < 2 || class > 5 {
		return codes.InternalServerError
	}
	if detail > 31 {
		detail = 0
	}
	return codes.Code(class<<5 | detail)
}

// Core tokens are 8 random bytes; on MQTT they travel as a uint (T §8.3).
// ponytail: assumes the core's 8-byte tokens; map tokens if that changes.
func tokenUint(t []byte) uint64 {
	var b [8]byte
	copy(b[8-min(len(t), 8):], t)
	return binary.BigEndian.Uint64(b[:])
}

func tokenBytes(u uint64) []byte { return binary.BigEndian.AppendUint64(nil, u) }

// peer is the Peer of one endpoint topic.
type peer struct {
	b  *Binding
	ep string
}

type addr string

func (a addr) Network() string { return "mqtt" }
func (a addr) String() string  { return string(a) }

func (p *peer) Binding() string      { return "M" }
func (p *peer) RemoteAddr() net.Addr { return addr(p.b.Topic(p.ep)) }

// Identity: the broker, not the Server, authenticates MQTT clients, so the
// Server only knows the topic the client speaks on.
func (p *peer) Identity() server.Identity {
	return server.Identity{Mode: server.ModeNoSec, Addr: "mqtt:" + p.b.Topic(p.ep)}
}

// Exchange maps a downlink request to its MQTT operation (T §8.3.3,
// §8.3.4), publishes it and waits for the result with the same token.
func (p *peer) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	down := downlink
	if p.b.bs != nil {
		down = bootstrapDownlink
	}
	m, err := down(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan *Message, 1)
	p.b.mu.Lock()
	p.b.pending[m.Token] = ch
	p.b.mu.Unlock()
	defer func() {
		p.b.mu.Lock()
		delete(p.b.pending, m.Token)
		p.b.mu.Unlock()
	}()
	if err := p.b.publish(p.ep, m); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		out := &server.Message{Code: Code(*r.Result), Token: req.Token, Format: ctOf(r), Payload: r.Payload}
		if req.Observe != nil && *req.Observe == 0 {
			out.Observe = u32(0)
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func u32(v uint32) *uint32 { return &v }

var errUnsupported = errors.New("mqttbinding: not expressible in LwM2M over MQTT")

// downlink builds the MQTT request for a Server request.
func downlink(req *server.Message) (*Message, error) {
	m := &Message{Token: tokenUint(req.Token), URI: str(req.Path)}
	op := func(o uint64) { m.Operation = u64(o) }
	var ct *uint64
	if req.Format != nil {
		ct = u64(uint64(*req.Format))
	}
	observe := -1
	if req.Observe != nil {
		observe = int(*req.Observe)
	}
	// MQTT has no Accept key: the client picks the format and the core
	// decodes any of them (C §7.5).
	switch req.Code {
	case codes.GET:
		switch {
		case observe == 0:
			if len(req.Query) > 0 {
				return nil, fmt.Errorf("%w: attributes in Observe (T Tbl 8.3.4-1 has none)", errUnsupported)
			}
			op(OpObserve)
		case observe == 1:
			op(OpCancelObserve)
			m.URI = nil
		case req.Accept != nil && *req.Accept == lwm2m.FormatLinkFormat:
			op(OpDiscover)
			for _, q := range req.Query {
				if v, ok := strings.CutPrefix(q, "depth="); ok {
					d, err := strconv.ParseUint(v, 10, 8)
					if err != nil || d > 3 {
						return nil, fmt.Errorf("%w: depth %q", errUnsupported, v)
					}
					m.Depth = u64(d)
				}
			}
		default:
			op(OpRead)
		}
	case 5: // FETCH
		m.URI = nil
		switch observe {
		case 0:
			op(OpObserveComposite)
		case 1:
			op(OpCancelObserve)
			return m, nil
		default:
			op(OpReadComposite)
		}
		paths, err := etchPaths(req)
		if err != nil {
			return nil, err
		}
		m.Paths = paths
	case 7: // iPATCH
		op(OpWriteComposite)
		m.URI, m.CT, m.Payload = nil, ct, req.Payload
	case codes.PUT:
		if req.Format != nil {
			op(OpWriteReplace)
			m.CT, m.Payload = ct, req.Payload
			break
		}
		op(OpWriteAttributes)
		if err := attributes(m, req.Query); err != nil {
			return nil, err
		}
	case codes.POST:
		switch n := numericSegments(req.Path); {
		case n == 1:
			op(OpCreate)
			m.CT, m.Payload = ct, req.Payload
		case n == 3 && (req.Format == nil || *req.Format == lwm2m.FormatText):
			op(OpExecute)
			m.Payload = req.Payload
		default:
			op(OpWritePartial)
			m.CT, m.Payload = ct, req.Payload
		}
	case codes.DELETE:
		op(OpDelete)
	default:
		return nil, fmt.Errorf("%w: method %v", errUnsupported, req.Code)
	}
	return m, nil
}

// numericSegments counts the LwM2M path segments after any alternate path
// (which has no numeric segment, T §6.4.1).
func numericSegments(p string) int {
	n := 0
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if _, err := strconv.ParseUint(s, 10, 16); err == nil {
			n++
		}
	}
	return n
}

// etchPaths re-encodes a composite path list as SenML-ETCH CBOR, the only
// form key 6 carries (T §8.3.3).
func etchPaths(req *server.Message) ([]byte, error) {
	if req.Format == nil {
		return nil, fmt.Errorf("%w: composite request without a path list format", errUnsupported)
	}
	c, err := codec.For(*req.Format)
	if err != nil {
		return nil, err
	}
	sc, ok := c.(senml.Codec)
	if !ok {
		return nil, fmt.Errorf("%w: path list in %v", errUnsupported, *req.Format)
	}
	paths, err := sc.DecodePaths(req.Payload)
	if err != nil {
		return nil, err
	}
	return senml.ETCHCBOR.EncodePaths(paths)
}

// attributes maps Write-Attributes query items to keys 12-21. lt and con
// have no key and a valueless (unset) item has no encoding (A-7), so they
// are refused rather than silently dropped.
func attributes(m *Message, query []string) error {
	for _, q := range query {
		k, v, ok := strings.Cut(q, "=")
		if !ok {
			return fmt.Errorf("%w: unsetting %q", errUnsupported, k)
		}
		var err error
		uint_ := func(dst **uint64) {
			var n uint64
			n, err = strconv.ParseUint(v, 10, 64)
			*dst = &n
		}
		float := func(dst **float64) {
			var f float64
			f, err = strconv.ParseFloat(v, 64)
			*dst = &f
		}
		switch k {
		case "pmin":
			uint_(&m.Pmin)
		case "pmax":
			uint_(&m.Pmax)
		case "gt":
			float(&m.Gt)
		case "st":
			float(&m.St)
		case "epmin":
			uint_(&m.Epmin)
		case "epmax":
			uint_(&m.Epmax)
		case "edge":
			uint_(&m.Edge)
		case "hqmax":
			uint_(&m.Hqmax)
		default:
			return fmt.Errorf("%w: attribute %q has no CBOR key", errUnsupported, k)
		}
		if err != nil {
			return fmt.Errorf("%w: %s=%s", errUnsupported, k, v)
		}
	}
	return nil
}
