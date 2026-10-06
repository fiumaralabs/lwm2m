package server

import (
	"context"
	"net"
	"net/http"
	"slices"
	"sync"

	"github.com/fiumaralabs/lwm2m/internal/coapws"
	"github.com/gorilla/websocket"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
)

// CoAP over WebSockets (RFC 8323 §4, T §6.8.6, TCP-03): coap+ws on an
// http.Server, coaps+ws when that server runs TLS. The WebSocket carries
// the RFC 8323 TCP-style messages without the length field; coapws
// restores it, so each socket is an ordinary binding-T connection served
// by serveTCP: CSM first, Ping/Pong, requests back over the same socket,
// notifications without ACK and explicit cancel (RFC 8323 §5, §7).

// WebSocketHandler returns an http.Handler that upgrades requests offering
// the "coap" subprotocol (RFC 8323 §4.1) to CoAP; others get 400. Each call
// starts its own acceptor, stopped by Close.
func (s *Server) WebSocketHandler() http.Handler {
	l := &wsListener{conns: make(chan net.Conn), done: make(chan struct{})}
	s.serveTCP(l)
	up := websocket.Upgrader{Subprotocols: []string{coapws.Subprotocol}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(websocket.Subprotocols(r), coapws.Subprotocol) {
			http.Error(w, `WebSocket subprotocol "coap" required (RFC 8323 §4.1)`, http.StatusBadRequest)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade wrote the error response
		}
		select {
		case l.conns <- coapws.NewConn(ws):
		case <-l.done:
			_ = ws.Close()
		case <-r.Context().Done():
			_ = ws.Close()
		}
	})
}

// wsListener hands upgraded sockets to go-coap's TCP server.
type wsListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *wsListener) AcceptWithContext(ctx context.Context) (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, coapnet.ErrListenerIsClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *wsListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}
