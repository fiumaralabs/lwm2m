// Package dtlscoap runs go-coap over github.com/fiumaralabs/dtls/v3, the
// LwM2M-patched pion/dtls fork. go-coap's own DTLS helpers
// (net.NewDTLSListener, dtls.Dial, dtls.Client) are typed against upstream
// github.com/fiumaralabs/dtls/v3, but its DTLS server only needs a Listener that
// yields net.Conns, and its client is built from exported parts, so these
// few functions are all the fork needs. Remove this package once the
// patches are upstream and go-coap's helpers can be used again.
package dtlscoap

import (
	"context"
	"fmt"
	"net"
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

	"github.com/fiumaralabs/dtls/v3"
	dtlsnet "github.com/fiumaralabs/dtls/v3/pkg/net"
)

// Listener is a DTLS listener for go-coap's dtls Server.Serve, the
// counterpart of go-coap's net.DTLSListener.
type Listener struct {
	l      net.Listener
	closed atomic.Bool
}

// Listen listens for DTLS on a UDP address ("udp", "udp4" or "udp6").
func Listen(network, addr string, cfg *dtls.Config) (*Listener, error) {
	a, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve address: %w", err)
	}
	l, err := dtls.Listen(network, a, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create dtls listener: %w", err)
	}
	return &Listener{l: l}, nil
}

// AcceptWithContext returns the next connection; its handshake runs when
// go-coap calls HandshakeContext on it.
func (l *Listener) AcceptWithContext(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.closed.Load() {
		return nil, coapnet.ErrListenerIsClosed
	}
	return l.l.Accept()
}

// Close stops the listener.
func (l *Listener) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	return l.l.Close()
}

// Addr is the local address.
func (l *Listener) Addr() net.Addr { return l.l.Addr() }

// Dial is go-coap's dtls.Dial for a fork config.
func Dial(target string, cfg *dtls.Config, opts ...udp.Option) (*udpClient.Conn, error) {
	c := coapdtls.DefaultConfig
	for _, o := range opts {
		o.UDPClientApply(&c)
	}
	nc, err := c.Dialer.DialContext(c.Ctx, c.Net, target)
	if err != nil {
		return nil, err
	}
	conn, err := dtls.Client(dtlsnet.PacketConnFromConn(nc), nc.RemoteAddr(), cfg)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	return Client(conn, append(opts, options.WithCloseSocket())...), nil
}

// Client is go-coap's dtls.Client (v3.5.4) for a fork connection.
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
