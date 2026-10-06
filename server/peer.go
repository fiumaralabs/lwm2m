package server

import (
	"context"
	"net"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Message is a binding-neutral LwM2M request or response. CoAP maps it to
// options one to one; the MQTT and HTTP bindings (T §7, §8) map it to
// their own framing.
type Message struct {
	Code     codes.Code // method or response code (CoAP numbering)
	Path     string     // request URI path, "/" for the root
	Query    []string   // "k=v" or "k"
	Format   *lwm2m.ContentFormat
	Accept   *lwm2m.ContentFormat
	Observe  *uint32
	Token    []byte
	Payload  []byte
	Location []string // response Location-Path segments
}

// Peer is one transport session with a client: a CoAP/UDP or DTLS
// endpoint, a CoAP/TCP connection, or an MQTT or HTTP client. Every
// binding implements it so the LwM2M core stays binding-agnostic.
type Peer interface {
	// Exchange sends a request to the client and returns its response.
	Exchange(ctx context.Context, req *Message) (*Message, error)
	// Identity is what the transport authenticated about the client.
	Identity() Identity
	// RemoteAddr is the client's address on this transport.
	RemoteAddr() net.Addr
	// Binding is the transport binding letter: U, T, S, N, M or H
	// (C Tbl 6.2.1.2-1).
	Binding() string
}

// isRequest reports a method code (0.01-0.31).
func (m *Message) isRequest() bool { return m.Code >= 1 && m.Code < 32 }

// segments splits the path into its segments ("/" has none).
func (m *Message) segments() []string {
	p := strings.Trim(m.Path, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func status(code codes.Code) *Message { return &Message{Code: code} }

// HandleUplink processes a message the client sent on peer: Register,
// Update, De-register or Send requests (C §6.2, §6.4.6), or a notification
// for an observation (C §6.4.2). It returns the response to send (nil for
// a notification: the binding just acknowledges it) and a function the
// binding must call once the response is sent, so events and requests
// they trigger follow the reply (GEN-10). after is never nil.
func (s *Server) HandleUplink(peer Peer, m *Message) (resp *Message, after func()) {
	r := s.uplink(peer, m)
	if r.after == nil {
		r.after = func() {}
	}
	return r.msg, r.after
}

func (s *Server) uplink(peer Peer, m *Message) reply {
	if !m.isRequest() {
		s.handleNotification(peer, m)
		return reply{}
	}
	seg := m.segments()
	switch {
	case len(seg) == 1 && seg[0] == "rd":
		if m.Code != codes.POST {
			return replyCode(codes.MethodNotAllowed)
		}
		return s.register(peer, m)
	case len(seg) == 2 && seg[0] == "rd":
		return s.location(peer, m, seg[1])
	case len(seg) == 1 && seg[0] == "dp":
		if m.Code != codes.POST {
			return replyCode(codes.MethodNotAllowed)
		}
		return s.send(peer, m)
	}
	return replyCode(codes.NotFound)
}
