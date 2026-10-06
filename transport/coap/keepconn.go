package coap

import (
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
// Found by the Zephyr interop suite: queue-mode and idle clients. The core
// tracks which sessions carry a registration (Server.Registered).
type heldConns struct {
	idle time.Duration // 0 = idleClose; set before Listen*
}

// monitor is the inactivity option for the UDP and DTLS listeners. A held
// connection is checked again on every tick until its registration ends.
func (b *Binding) monitor() options.InactivityMonitorOpt[func(*client.Conn)] {
	idle := b.held.idle
	if idle == 0 {
		idle = idleClose
	}
	return options.WithInactivityMonitor(idle, func(cc *client.Conn) {
		if p, ok := b.peers.Load(coapConn(cc)); !ok || !b.srv.Registered(p.(*coapPeer)) {
			_ = cc.Close()
		}
	})
}
