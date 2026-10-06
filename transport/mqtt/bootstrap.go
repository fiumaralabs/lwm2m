package mqtt

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strconv"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// NewBootstrap connects to the broker and serves the Bootstrap interface
// of bs (T §8.3.1, Tbl 8.3.1-1): it subscribes to {PREFIX}/lwm2m/bs/#,
// answers Bootstrap-Request (0) and Bootstrap-Pack-Request (6) published
// on {PREFIX}/lwm2m/bs/{ENDPOINT}, and sends the session's
// Bootstrap-Write (1), -Read (2), -Delete (3), -Discover (4) and -Finish
// (5) there. Results follow T Tbl 8.5-1. ENDPOINT is the endpoint name
// (T §8.2); the broker authenticates the client, so the session's
// identity is NoSec unless Config.COSE protects the endpoint. The default
// ClientID is "lwm2m-bs".
func NewBootstrap(bs *bootstrap.Server, cfg Config) (*Binding, error) {
	b := &Binding{bs: bs, cfg: cfg, pending: map[uint64]chan *Message{}, peers: map[string]*peer{}}
	if err := b.connect("bs", "lwm2m-bs"); err != nil {
		return nil, err
	}
	return b, nil
}

// bootstrapUplink handles a request published on a bs topic.
func (b *Binding) bootstrapUplink(ep string, m *Message) {
	if op := *m.Operation; op >= OpBootstrapWrite && op <= OpBootstrapFinish {
		return // our own downlink echoed back
	}
	req, code := bootstrapRequest(ep, m)
	resp, after := &server.Message{Code: code}, func() {}
	if code == 0 {
		resp, after = b.bs.HandleUplink(b.peer(ep), req)
	}
	b.respond(ep, m.Token, resp)
	after() // the session's first request follows the 204
}

// bootstrapRequest maps operation 0 to POST /bs?ep=&pct= and operation 6
// to GET /bspack?ep=&acc= (C §6.1.7.1, §6.1.7.7). The Pack-Request
// payload is acc, the BS-account instances as CoRE links; pct, which the
// generic Bootstrap_Payload allows on every operation, is its Accept.
func bootstrapRequest(ep string, m *Message) (*server.Message, codes.Code) {
	if m.EP != nil && *m.EP != ep {
		return nil, codes.BadRequest // ENDPOINT has to match the ep (T §8.2)
	}
	q := []string{"ep=" + ep}
	switch *m.Operation {
	case OpBootstrapRequest:
		if m.PCT != nil {
			q = append(q, "pct="+strconv.FormatUint(*m.PCT, 10))
		}
		return &server.Message{Code: codes.POST, Path: "/bs", Query: q}, 0
	case OpBootstrapPack:
		if len(m.Payload) > 0 {
			if m.CT != nil && *m.CT != uint64(lwm2m.FormatLinkFormat) {
				return nil, codes.BadRequest
			}
			q = append(q, "acc="+string(m.Payload))
		}
		req := &server.Message{Code: codes.GET, Path: "/bspack", Query: q}
		if m.PCT != nil {
			if *m.PCT > 0xffff {
				return nil, codes.NotAcceptable
			}
			f := lwm2m.ContentFormat(*m.PCT)
			req.Accept = &f
		}
		return req, 0
	}
	return nil, codes.NotImplemented // T Tbl 8.5-5
}

// bootstrapDownlink builds the MQTT request for a session request. The
// session sets no token, so each gets a random one: our own results to
// the client's requests come back on the shared topic and must not match
// a pending request.
func bootstrapDownlink(req *server.Message) (*Message, error) {
	var t [8]byte
	if _, err := rand.Read(t[:]); err != nil {
		return nil, err
	}
	m := &Message{Token: binary.BigEndian.Uint64(t[:]), URI: str(req.Path)}
	op := func(o uint64) { m.Operation = u64(o) }
	switch {
	case req.Code == codes.POST && req.Path == "/bs":
		op(OpBootstrapFinish)
		m.URI = nil
	case req.Code == codes.PUT && req.Format != nil:
		op(OpBootstrapWrite)
		m.CT, m.Payload = u64(uint64(*req.Format)), req.Payload
	case req.Code == codes.DELETE:
		op(OpBootstrapDelete)
	case req.Code == codes.GET && req.Accept != nil && *req.Accept == lwm2m.FormatLinkFormat:
		op(OpBootstrapDiscover)
	case req.Code == codes.GET:
		op(OpBootstrapRead)
		if req.Accept != nil {
			m.PCT = u64(uint64(*req.Accept))
		}
	default:
		return nil, fmt.Errorf("%w: bootstrap %v %s", errUnsupported, req.Code, req.Path)
	}
	return m, nil
}
