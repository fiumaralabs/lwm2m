// Package dtlscoap runs go-coap over github.com/pion/dtls/v4. go-coap's
// own DTLS helpers (net.NewDTLSListener, dtls.Dial, dtls.Client) are typed
// against pion/dtls/v3, but its DTLS server only needs a Listener that
// yields net.Conns, and its client is built from exported parts, so these
// few functions are all v4 needs.
//
// The listener has its own UDP demux (demux.go) and restores the PSK
// identity of resumed sessions (SessionStore): two things upstream pion
// does not do (interop/upstream-pion.md).
package dtlscoap

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/dtls/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/net/monitor/inactivity"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
	udpClient "github.com/plgd-dev/go-coap/v3/udp/client"

	lwm2mdtls "github.com/fiumaralabs/lwm2m/security/dtls"
	"github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	"github.com/pion/dtls/v4/pkg/protocol"
)

// Config configures a Listener.
type Config struct {
	Options []dtls.ServerOption
	// CIDLength is the length of the Connection IDs the server assigns
	// (RFC 9146, RFC 9147 §9); 0 disables CID. The listener owns CID
	// generation, so a dtls.WithConnectionID in Options is overridden.
	CIDLength int
	// Sessions enables session resumption; nil disables it.
	Sessions *SessionStore
}

// Listener is a DTLS listener for go-coap's dtls Server.Serve, the
// counterpart of go-coap's net.DTLSListener.
type Listener struct {
	d      *demux
	cfg    Config
	closed atomic.Bool
}

// Listen listens for DTLS on a UDP address ("udp", "udp4" or "udp6").
func Listen(network, addr string, cfg Config) (*Listener, error) {
	if _, err := dtls.NewListener(nil, cfg.Options...); err != nil { // validates the options
		return nil, fmt.Errorf("cannot create dtls listener: %w", err)
	}
	a, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve address: %w", err)
	}
	pc, err := net.ListenUDP(network, a)
	if err != nil {
		return nil, fmt.Errorf("cannot create dtls listener: %w", err)
	}
	return &Listener{d: newDemux(pc, cfg.CIDLength), cfg: cfg}, nil
}

// AcceptWithContext returns the next connection; its handshake runs when
// go-coap calls HandshakeContext on it.
func (l *Listener) AcceptWithContext(ctx context.Context) (net.Conn, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-l.d.done:
			return nil, coapnet.ErrListenerIsClosed
		case pc := <-l.d.accept:
			opts := l.cfg.Options[:len(l.cfg.Options):len(l.cfg.Options)]
			if l.cfg.CIDLength > 0 {
				opts = append(opts, dtls.WithConnectionID(func() []byte { return l.d.newCID(pc) }, dtls.CIDPathMigrationUnsafe))
			}
			if l.cfg.Sessions != nil {
				opts = append(opts, dtls.WithSessionStore(l.cfg.Sessions))
			}
			dc, err := dtls.Server(pc, pc.raddr, opts...)
			if err != nil {
				_ = pc.Close()
				continue
			}
			return &Conn{Conn: dc, pc: pc, sessions: l.cfg.Sessions}, nil
		}
	}
}

// Close stops the listener and closes its connections.
func (l *Listener) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	return l.d.close()
}

// Addr is the local address.
func (l *Listener) Addr() net.Addr { return l.d.pc.LocalAddr() }

// Conn is a server-side DTLS connection of a Listener.
type Conn struct {
	*dtls.Conn
	pc       *packetConn
	sessions *SessionStore

	mu       sync.Mutex
	identity []byte // PSK identity restored on resumption
}

// HandshakeContext runs the handshake, then lets the connection take over
// its address if it replaces an association (demux) and records or
// restores the session's PSK identity.
func (c *Conn) HandshakeContext(ctx context.Context) error {
	if err := c.Conn.HandshakeContext(ctx); err != nil {
		return err
	}
	c.pc.d.handshakeComplete(c.pc)
	if st, ok := c.Conn.ConnectionState(); ok && c.sessions != nil && len(st.SessionID) > 0 {
		if len(st.IdentityHint) > 0 {
			c.sessions.setIdentity(st.SessionID, st.IdentityHint)
		} else {
			c.mu.Lock()
			c.identity = c.sessions.identity(st.SessionID)
			c.mu.Unlock()
		}
	}
	return nil
}

// ConnectionState is pion's, with the PSK identity of a resumed session.
func (c *Conn) ConnectionState() (dtls.State, bool) {
	st, ok := c.Conn.ConnectionState()
	if ok && len(st.IdentityHint) == 0 {
		c.mu.Lock()
		st.IdentityHint = c.identity
		c.mu.Unlock()
	}
	return st, ok
}

// Dial is go-coap's dtls.Dial for pion/dtls v4. The handshake runs on
// first use.
func Dial(target string, dtlsOpts []dtls.ClientOption, opts ...udp.Option) (*udpClient.Conn, error) {
	a, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	conn, err := dtls.Dial("udp", a, dtlsOpts...)
	if err != nil {
		return nil, err
	}
	return Client(conn, append(opts, options.WithCloseSocket())...), nil
}

// Client is go-coap's dtls.Client (v3.5.4) for a pion/dtls v4 connection.
func Client(conn *dtls.Conn, opts ...udp.Option) *udpClient.Conn {
	cfg := coapdtls.DefaultConfig
	for _, o := range opts {
		o.UDPClientApply(&cfg)
	}
	if cfg.Errors == nil {
		cfg.Errors = func(error) {}
	}
	if cfg.CreateInactivityMonitor == nil {
		cfg.CreateInactivityMonitor = func() udpClient.InactivityMonitor {
			return inactivity.NewNilMonitor[*udpClient.Conn]()
		}
	}
	if cfg.MessagePool == nil {
		cfg.MessagePool = pool.New(0, 0)
	}
	errorsFunc := cfg.Errors
	cfg.Errors = func(err error) {
		if coapnet.IsCancelOrCloseError(err) {
			return
		}
		errorsFunc(fmt.Errorf("dtls: %v: %w", conn.RemoteAddr(), err))
	}
	createBlockWise := func(*udpClient.Conn) *blockwise.BlockWise[*udpClient.Conn] { return nil }
	if cfg.BlockwiseEnable {
		createBlockWise = func(cc *udpClient.Conn) *blockwise.BlockWise[*udpClient.Conn] {
			return blockwise.New(cc, cfg.BlockwiseTransferTimeout, cfg.Errors,
				func(token message.Token) (*pool.Message, bool) { return cc.GetObservationRequest(token) })
		}
	}
	session := server.NewSession(cfg.Ctx, coapnet.NewConn(conn), cfg.MaxMessageSize, cfg.MTU, cfg.CloseSocket)
	cc := udpClient.NewConnWithOpts(session, &cfg,
		udpClient.WithBlockWise(createBlockWise),
		udpClient.WithInactivityMonitor(cfg.CreateInactivityMonitor()),
		udpClient.WithRequestMonitor(cfg.RequestMonitor),
	)
	cfg.PeriodicRunner(func(now time.Time) bool {
		cc.CheckExpirations(now)
		return cc.Context().Err() == nil
	})
	go func() {
		if err := cc.Run(); err != nil {
			cfg.Errors(err)
		}
	}()
	return cc
}

// CipherSuites are the suites a listener accepts by default: the LwM2M
// mandatory PSK and ECDHE_ECDSA suites (0xC023 from security/dtls), more
// AEAD suites, and TLS_AES_128_GCM_SHA256 for DTLS 1.3.
func CipherSuites() []ciphersuite.ID {
	return []ciphersuite.ID{
		ciphersuite.TLS_PSK_WITH_AES_128_CCM_8,
		ciphersuite.TLS_PSK_WITH_AES_128_CBC_SHA256,
		ciphersuite.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8,
		lwm2mdtls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
		ciphersuite.TLS_PSK_WITH_AES_128_CCM,
		ciphersuite.TLS_PSK_WITH_AES_128_GCM_SHA256,
		ciphersuite.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		ciphersuite.TLS_AES_128_GCM_SHA256,
	}
}

// ServerConfig is a listener Config with the defaults: psk (if not nil)
// as the PSK lookup, CipherSuites, DTLS 1.2 and 1.3 (pion negotiates),
// then opts, which win over the defaults. sessionTTL > 0 enables
// resumption.
func ServerConfig(opts []dtls.ServerOption, cidLength int, sessionTTL time.Duration, psk dtls.PSKCallback) Config {
	defaults := []dtls.ServerOption{
		dtls.WithCipherSuites(CipherSuites()...),
		dtls.WithCustomCipherSuites(lwm2mdtls.Custom),
		dtls.WithMaxVersion(protocol.Version1_3),
	}
	if psk != nil {
		defaults = append(defaults, dtls.WithPSK(psk))
	}
	cfg := Config{CIDLength: cidLength, Options: append(defaults, opts...)}
	if sessionTTL > 0 {
		cfg.Sessions = NewSessionStore(sessionTTL, 100_000, time.Now)
	}
	return cfg
}
