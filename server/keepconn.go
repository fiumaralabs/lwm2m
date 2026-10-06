package server

import (
	"sync"
	"time"

	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// idleClose is how long a UDP/DTLS connection with no registration on it
// may stay silent (go-coap's default).
const idleClose = 16 * time.Second

// heldConns keeps the connections that carry a live registration open.
// go-coap closes a UDP/DTLS connection after 16 s without traffic; for a
// registered client that discards its DTLS session (and Connection ID)
// between Updates, so every later downlink fails and the client has to
// handshake again (T §5.2.8: the session lasts for the registration).
// Found by the Zephyr interop suite: queue-mode and idle clients.
type heldConns struct {
	m    sync.Map      // coapConn -> struct{}
	idle time.Duration // 0 = idleClose; set before Listen*
}

func (h *heldConns) track(e Event) {
	switch e := e.(type) {
	case Registered:
		h.hold(e.Registration.peer)
		if e.Replaced != nil && e.Replaced.peer != e.Registration.peer {
			h.release(e.Replaced.peer)
		}
	case Updated:
		h.hold(e.Registration.peer)
		if e.Previous.peer != e.Registration.peer {
			h.release(e.Previous.peer)
		}
	case Deregistered: // client, expiry, replacement, operator
		h.release(e.Registration.peer)
	}
}

func (h *heldConns) hold(p Peer) {
	if cp, ok := p.(*coapPeer); ok {
		h.m.Store(cp.cc, struct{}{})
	}
}

func (h *heldConns) release(p Peer) {
	if cp, ok := p.(*coapPeer); ok {
		h.m.Delete(cp.cc)
	}
}

// monitor is the inactivity option for the UDP and DTLS listeners. A held
// connection is checked again on every tick until its registration ends.
func (h *heldConns) monitor() options.InactivityMonitorOpt[func(*client.Conn)] {
	idle := h.idle
	if idle == 0 {
		idle = idleClose
	}
	return options.WithInactivityMonitor(idle, func(cc *client.Conn) {
		if _, held := h.m.Load(coapConn(cc)); !held {
			_ = cc.Close()
		}
	})
}
