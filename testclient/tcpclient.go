package testclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/tcp"
	tcpClient "github.com/plgd-dev/go-coap/v3/tcp/client"
)

// TCPClient is a test client on binding T: CoAP over TCP or TLS
// (RFC 8323). It serves Device Management from the embedded Client's store
// and sends uplinks over its own connection.
//
// ponytail: Client's own notify path writes to its UDP conn, so a server
// Write to an observed path would panic here; tests call Notify instead.
type TCPClient struct {
	*Client
	conn *tcpClient.Conn
}

// NewTCP returns a TCP test client with an empty store.
func NewTCP(cfg Config) *TCPClient { return &TCPClient{Client: New(cfg)} }

// Dial connects to addr, over TLS when tlsCfg is non-nil. It fails unless
// the server's CSM arrives within a second (RFC 8323 §5.3).
func (t *TCPClient) Dial(addr string, tlsCfg *tls.Config) error {
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(t.handle))
	opts := []tcp.Option{
		options.WithMux(r),
		options.WithBlockwise(true, 0x6, 30*time.Second),
		options.WithCSMExchangeTimeout(time.Second),
	}
	if tlsCfg != nil {
		opts = append(opts, options.WithTLS(tlsCfg))
	}
	conn, err := tcp.Dial(addr, opts...)
	if err != nil {
		return err
	}
	t.conn = conn
	return nil
}

// Close closes the connection.
func (t *TCPClient) Close() error {
	if t.conn == nil {
		return nil
	}
	return t.conn.Close()
}

// LocalAddr is the client's source address.
func (t *TCPClient) LocalAddr() net.Addr { return t.conn.LocalAddr() }

// Ping sends a CoAP Ping and waits for the Pong (RFC 8323 §5.4).
func (t *TCPClient) Ping(ctx context.Context) error { return t.conn.Ping(ctx) }

// Set stores a value without notifying observers.
func (t *TCPClient) Set(p lwm2m.Path, v lwm2m.Value) {
	t.mu.Lock()
	t.setLocked(p, v)
	t.mu.Unlock()
}

// Register registers with the default query and object list.
func (t *TCPClient) Register(ctx context.Context) (*Response, error) {
	cf := lwm2m.FormatLinkFormat
	r, err := t.Raw(ctx, codes.POST, "/rd", t.RegisterQuery(), &cf, []byte(t.ObjectLinks()))
	if err == nil && r.Code == codes.Created && len(r.Location) == 2 {
		t.mu.Lock()
		t.location = r.Location[1]
		t.observers = map[string]*observer{}
		t.mu.Unlock()
	}
	return r, err
}

// Update sends POST /rd/<location>.
func (t *TCPClient) Update(ctx context.Context, query []string) (*Response, error) {
	return t.Raw(ctx, codes.POST, "/rd/"+t.Location(), query, nil, nil)
}

// Deregister sends DELETE /rd/<location>.
func (t *TCPClient) Deregister(ctx context.Context) (*Response, error) {
	return t.Raw(ctx, codes.DELETE, "/rd/"+t.Location(), nil, nil, nil)
}

// Send sends nodes to /dp in format cf (C §6.4.6).
func (t *TCPClient) Send(ctx context.Context, nodes []lwm2m.Node, cf lwm2m.ContentFormat) (*Response, error) {
	cd, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	body, err := cd.Encode(lwm2m.Root, nodes)
	if err != nil {
		return nil, err
	}
	return t.Raw(ctx, codes.POST, "/dp", nil, &cf, body)
}

// Raw sends an arbitrary request to the server.
func (t *TCPClient) Raw(ctx context.Context, code codes.Code, path string, query []string, cf *lwm2m.ContentFormat, body []byte) (*Response, error) {
	m := t.conn.AcquireMessage(ctx)
	defer t.conn.ReleaseMessage(m)
	m.SetCode(code)
	if err := m.SetPath(path); err != nil {
		return nil, err
	}
	tok, err := message.GetToken()
	if err != nil {
		return nil, err
	}
	m.SetToken(tok)
	for _, q := range query {
		m.AddQuery(q)
	}
	if cf != nil {
		m.SetContentFormat(message.MediaType(*cf))
	}
	if body != nil {
		m.SetBody(bytes.NewReader(body))
	}
	res, err := t.conn.Do(m)
	if err != nil {
		return nil, err
	}
	defer t.conn.ReleaseMessage(res)
	out := &Response{Code: res.Code()}
	if lp, err := res.Options().LocationPath(); err == nil && lp != "" {
		out.Location = strings.Split(strings.Trim(lp, "/"), "/")
	}
	if res.Body() != nil {
		out.Body, _ = io.ReadAll(res.Body())
	}
	return out, nil
}

// Notify sends a notification with Observe value seq for token tok (the
// observation's path, or /3/0 for a token the client does not hold). Over
// TCP there is no ACK or Reset (RFC 8323 §7.2); seq 0 sends an empty
// Observe value (§7.1).
func (t *TCPClient) Notify(ctx context.Context, tok message.Token, seq uint32) error {
	t.mu.Lock()
	o, ok := t.observers[string(tok)]
	t.mu.Unlock()
	base := lwm2m.MustParsePath("/3/0")
	var accept *lwm2m.ContentFormat
	if ok {
		base, accept = o.paths[0], o.accept
	}
	nodes := t.Nodes(base)
	_, cf, body, _ := t.encode(base, t.responseFormat(base, accept, nodes), nodes)
	if cf == nil {
		return fmt.Errorf("testclient: cannot encode notification")
	}
	m := t.conn.AcquireMessage(ctx)
	defer t.conn.ReleaseMessage(m)
	m.SetCode(codes.Content)
	m.SetToken(tok)
	m.SetObserve(seq)
	m.SetContentFormat(message.MediaType(*cf))
	m.SetBody(bytes.NewReader(body))
	return t.conn.WriteMessage(m)
}
