// Package coapwire carries CoAP datagrams (RFC 7252 §3 framing) over a
// transport that only moves opaque payloads: SMS (T §6.8.3), Non-IP data
// (T §6.8.5) and LoRaWAN (T §6.8.4). Conn is the server.Peer of one remote
// endpoint; the binding feeds it inbound payloads and gives it a send
// function.
package coapwire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

// Frame is one CoAP message with its datagram header fields.
type Frame struct {
	Type message.Type
	MID  uint16
	Msg  server.Message
}

// Marshal encodes f with the UDP framing.
func Marshal(f Frame) ([]byte, error) {
	m := pool.NewMessage(context.Background())
	m.SetType(f.Type)
	m.SetMessageID(int32(f.MID))
	m.SetCode(f.Msg.Code)
	if f.Msg.Path != "" && f.Msg.Path != "/" {
		if err := m.SetPath(f.Msg.Path); err != nil {
			return nil, err
		}
	}
	for _, q := range f.Msg.Query {
		m.AddQuery(q)
	}
	if f.Msg.Token != nil {
		m.SetToken(f.Msg.Token)
	}
	if f.Msg.Observe != nil {
		m.SetObserve(*f.Msg.Observe)
	}
	if f.Msg.Accept != nil {
		m.SetAccept(message.MediaType(*f.Msg.Accept))
	}
	if f.Msg.Format != nil {
		m.SetContentFormat(message.MediaType(*f.Msg.Format))
	}
	for _, l := range f.Msg.Location {
		m.AddOptionString(message.LocationPath, l)
	}
	if len(f.Msg.Payload) > 0 {
		m.SetBody(bytes.NewReader(f.Msg.Payload))
	}
	b, err := m.MarshalWithEncoder(coder.DefaultCoder)
	return append([]byte(nil), b...), err
}

// Unmarshal decodes a UDP-framed CoAP message.
func Unmarshal(b []byte) (Frame, error) {
	m := pool.NewMessage(context.Background())
	if _, err := m.UnmarshalWithDecoder(coder.DefaultCoder, b); err != nil {
		return Frame{}, err
	}
	f := Frame{Type: m.Type(), MID: uint16(m.MessageID())}
	out := &f.Msg
	out.Code = m.Code()
	if len(m.Token()) > 0 {
		out.Token = append([]byte(nil), m.Token()...)
	}
	out.Path = "/"
	if p, err := m.Options().Path(); err == nil {
		out.Path = "/" + strings.TrimPrefix(p, "/")
	}
	out.Query, _ = m.Options().Queries()
	if cf, err := m.ContentFormat(); err == nil {
		v := lwm2m.ContentFormat(cf)
		out.Format = &v
	}
	if a, err := m.Options().Accept(); err == nil {
		v := lwm2m.ContentFormat(a)
		out.Accept = &v
	}
	if o, err := m.Observe(); err == nil {
		out.Observe = &o
	}
	if lp, err := m.Options().LocationPath(); err == nil && lp != "" {
		out.Location = strings.Split(strings.Trim(lp, "/"), "/")
	}
	if m.Body() != nil {
		p, err := io.ReadAll(m.Body())
		if err != nil {
			return Frame{}, err
		}
		out.Payload = p
	}
	return f, nil
}

var (
	ErrTimeout  = errors.New("coapwire: no response")
	ErrReset    = errors.New("coapwire: request reset by the client")
	ErrTooLarge = errors.New("coapwire: message exceeds the transport limit")
)

// Config configures a Conn.
type Config struct {
	Server   *server.Server
	Binding  string // Peer.Binding letter
	Identity server.Identity
	Addr     net.Addr
	// Send transmits one encoded message. A binding whose network reports
	// delivery (LoRaWAN Network Server, T App. C.3.2.2) returns only once
	// the message was sent, so AckTimeout starts at delivery.
	Send func(ctx context.Context, b []byte) error
	// AckTimeout is how long to wait for the response after each Send.
	// 0 waits for the context only.
	AckTimeout time.Duration
	// MaxRetransmit is how often a CON request is resent after
	// AckTimeout; 0 disables CoAP retransmission (SMS, T §6.8.3).
	MaxRetransmit int
	// Backoff doubles AckTimeout after each retransmission (RFC 7252
	// §4.2); off, every wait is AckTimeout (LoRaWAN, T App. C.3.2.2).
	Backoff bool
	// MaxSize, if non-zero, refuses outgoing messages above it (the
	// Non-IP MTU, CIOT-01).
	MaxSize int
	// Intercept, if set, sees every uplink request first. It may rewrite
	// m; a non-nil result is the reply and the core is skipped.
	Intercept func(m *server.Message) *server.Message
}

// Conn is the server.Peer of one remote endpoint.
type Conn struct {
	cfg     Config
	mu      sync.Mutex
	mid     uint16
	pending map[string]chan Frame // token -> response
	byMID   map[uint16]string     // outstanding CON MID -> token
	replies map[uint16][]byte     // recent uplink MID -> encoded reply (RFC 7252 §4.5)
	order   []uint16
}

func NewConn(cfg Config) *Conn {
	return &Conn{cfg: cfg, mid: uint16(rand.Uint32()), pending: map[string]chan Frame{},
		byMID: map[uint16]string{}, replies: map[uint16][]byte{}}
}

func (c *Conn) Identity() server.Identity { return c.cfg.Identity }
func (c *Conn) RemoteAddr() net.Addr      { return c.cfg.Addr }
func (c *Conn) Binding() string           { return c.cfg.Binding }

// NextMID returns a fresh message ID.
func (c *Conn) NextMID() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mid++
	return c.mid
}

func (c *Conn) send(ctx context.Context, f Frame) ([]byte, error) {
	b, err := Marshal(f)
	if err != nil {
		return nil, err
	}
	if c.cfg.MaxSize > 0 && len(b) > c.cfg.MaxSize {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrTooLarge, len(b), c.cfg.MaxSize)
	}
	return b, c.cfg.Send(ctx, b)
}

// Exchange sends req as a CON request and waits for its piggybacked or
// separate response.
func (c *Conn) Exchange(ctx context.Context, req *server.Message) (*server.Message, error) {
	f := Frame{Type: message.Confirmable, MID: c.NextMID(), Msg: *req}
	tok := string(req.Token)
	ch := make(chan Frame, 2)
	c.mu.Lock()
	c.pending[tok], c.byMID[f.MID] = ch, tok
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, tok)
		delete(c.byMID, f.MID)
		c.mu.Unlock()
	}()
	b, err := c.send(ctx, f)
	if err != nil {
		return nil, err
	}
	acked, wait := false, c.cfg.AckTimeout
	for tries := 0; ; {
		var timeout <-chan time.Time
		if wait > 0 && !acked {
			timeout = time.After(wait)
		}
		select {
		case r := <-ch:
			switch {
			case r.Type == message.Reset:
				return nil, ErrReset
			case r.Msg.Code == codes.Empty: // empty ACK: a separate response follows
				acked = true
				continue
			}
			out := r.Msg
			return &out, nil
		case <-timeout:
			if tries >= c.cfg.MaxRetransmit {
				return nil, ErrTimeout
			}
			tries++
			if c.cfg.Backoff {
				wait *= 2
			}
			if err := c.cfg.Send(ctx, b); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Receive handles one inbound CoAP message.
func (c *Conn) Receive(ctx context.Context, b []byte) error {
	f, err := Unmarshal(b)
	if err != nil {
		return err
	}
	m := &f.Msg
	switch {
	case f.Type == message.Acknowledgement || f.Type == message.Reset:
		c.mu.Lock()
		tok, ok := c.byMID[f.MID]
		ch := c.pending[tok]
		c.mu.Unlock()
		if ok && ch != nil {
			ch <- f
		}
		return nil
	case m.Code == codes.Empty: // CoAP ping (RFC 7252 §4.3), or a LoRaWAN RX-window opener
		if f.Type == message.Confirmable {
			_, err = c.send(ctx, Frame{Type: message.Reset, MID: f.MID})
		}
		return err
	case m.Code >= 64: // separate response or notification
		c.mu.Lock()
		ch := c.pending[string(m.Token)]
		c.mu.Unlock()
		switch {
		case ch != nil:
			ch <- f
		case m.Observe != nil && c.cfg.Server.KnownObservation(m.Token):
			c.cfg.Server.HandleUplink(c, m)
		case m.Observe != nil && f.Type != message.Acknowledgement:
			// Notification of an unknown observation: Reset (OBS-02).
			_, err = c.send(ctx, Frame{Type: message.Reset, MID: f.MID})
			return err
		}
		if f.Type == message.Confirmable {
			_, err = c.send(ctx, Frame{Type: message.Acknowledgement, MID: f.MID})
		}
		return err
	}
	// A request. A retransmitted CON gets the cached reply (RFC 7252 §4.5).
	c.mu.Lock()
	cached, dup := c.replies[f.MID]
	c.mu.Unlock()
	if dup && f.Type == message.Confirmable {
		return c.cfg.Send(ctx, cached)
	}
	var resp *server.Message
	after := func() {}
	if c.cfg.Intercept != nil {
		resp = c.cfg.Intercept(m)
	}
	if resp == nil {
		resp, after = c.cfg.Server.HandleUplink(c, m)
	}
	if resp == nil {
		resp = &server.Message{Code: codes.InternalServerError}
	}
	r := Frame{Type: message.Acknowledgement, MID: f.MID, Msg: *resp} // piggybacked (LORA-03)
	if f.Type != message.Confirmable {
		r.Type, r.MID = message.NonConfirmable, c.NextMID()
	}
	r.Msg.Token = m.Token
	enc, err := c.send(ctx, r)
	if err == nil && f.Type == message.Confirmable {
		c.mu.Lock()
		c.replies[f.MID] = enc
		c.order = append(c.order, f.MID)
		if len(c.order) > 32 { // ponytail: 32 recent MIDs, enough for EXCHANGE_LIFETIME on these slow links
			delete(c.replies, c.order[0])
			c.order = c.order[1:]
		}
		c.mu.Unlock()
	}
	after()
	return err
}

// Addr is the address of an endpoint on a non-IP transport: an MSISDN,
// a 3GPP External Identifier or a LoRaWAN DevEUI.
type Addr struct{ Net, ID string }

func (a Addr) Network() string { return a.Net }
func (a Addr) String() string  { return a.Net + ":" + a.ID }
