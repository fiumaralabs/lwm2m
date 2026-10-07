package dtlscoap

import (
	"bytes"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/dtls/v4/pkg/protocol"
	"github.com/pion/dtls/v4/pkg/protocol/handshake"
	"github.com/pion/dtls/v4/pkg/protocol/recordlayer"
	"github.com/pion/transport/v5/deadline"
)

// demux splits one UDP socket into one packetConn per DTLS association.
// It replaces pion's listener demux to handle a new handshake from the
// address of an established association (RFC 6347 §4.2.8, RFC 9147
// §5.11), which pion drops as a replay of the old one:
//
//   - The established association keeps the address until the new
//     handshake completes (a verified Finished), so a spoofed ClientHello
//     cannot take it over. Then the old association is closed.
//   - Meanwhile epoch 0 records go only to the new (pending) connection
//     and later epochs to both; each drops what it cannot authenticate
//     (RFC 9147 §5.11 "trial decryption"). pion marks a record as seen
//     only after it authenticates, so the old replay window is untouched.
//   - A ClientHello with a different client random replaces a pending
//     handshake, so a stuck or spoofed attempt cannot hold the address.
//     Retransmissions and the ClientHello answering a cookie keep the
//     random (RFC 6347 §4.2.1, RFC 8446 §4.1.2) and stay on it.
//
// Records with a Connection ID are routed by CID (RFC 9146, RFC 9147
// §9); the server assigns CIDs through a per-connection generator, so it
// knows which connection owns each.
type demux struct {
	pc     net.PacketConn
	cidLen int
	accept chan *packetConn
	done   chan struct{}
	once   sync.Once

	mu      sync.Mutex
	addrs   map[string]*packetConn // address owner
	pending map[string]*packetConn // new handshake on an owned address
	cids    map[string]*packetConn
}

const acceptBacklog = 128 // like pion's listener

func newDemux(pc net.PacketConn, cidLen int) *demux {
	d := &demux{pc: pc, cidLen: cidLen, accept: make(chan *packetConn, acceptBacklog), done: make(chan struct{}),
		addrs: map[string]*packetConn{}, pending: map[string]*packetConn{}, cids: map[string]*packetConn{}}
	go d.readLoop()
	return d
}

func (d *demux) close() error {
	var err error
	d.once.Do(func() {
		close(d.done)
		err = d.pc.Close()
		d.mu.Lock()
		var all []*packetConn
		for _, m := range []map[string]*packetConn{d.addrs, d.pending, d.cids} {
			for _, c := range m {
				all = append(all, c)
			}
		}
		d.mu.Unlock()
		for _, c := range all {
			_ = c.Close()
		}
	})
	return err
}

func (d *demux) readLoop() {
	buf := make([]byte, 8192) // pion's default receive buffer
	for {
		n, from, err := d.pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				_ = d.close()
				return
			}
			continue
		}
		for _, c := range d.route(from, buf[:n]) {
			c.deliver(buf[:n], from)
		}
	}
}

// route returns the connections that get datagram b from addr.
func (d *demux) route(from net.Addr, b []byte) []*packetConn {
	if d.cidLen > 0 {
		if cid := connectionID(b, d.cidLen); cid != nil {
			d.mu.Lock()
			c := d.cids[string(cid)]
			d.mu.Unlock()
			if c == nil {
				return nil // unknown CIDs are dropped, never routed by address
			}
			return []*packetConn{c}
		}
	}
	key := from.String()
	epoch0, isHandshake, random := classify(b)
	d.mu.Lock()
	defer d.mu.Unlock()
	c, p := d.addrs[key], d.pending[key]
	if c == nil {
		c, p = p, nil
	}
	if c == nil {
		if !isHandshake {
			return nil
		}
		if c = d.admit(from, ""); c == nil {
			return nil
		}
		d.addrs[key] = c
		return []*packetConn{c}
	}
	if !c.established.Load() {
		return []*packetConn{c}
	}
	if random != nil && (p == nil || p.random != string(random)) {
		if p != nil {
			d.closeLocked(p)
		}
		if p = d.admit(from, string(random)); p != nil {
			d.pending[key] = p
		} else {
			delete(d.pending, key)
		}
	}
	switch {
	case p == nil:
		return []*packetConn{c}
	case epoch0:
		return []*packetConn{p}
	default:
		return []*packetConn{c, p}
	}
}

// admit queues a new connection for Accept; nil when the backlog is full.
// d.mu is held.
func (d *demux) admit(from net.Addr, random string) *packetConn {
	c := &packetConn{d: d, key: from.String(), raddr: from, random: random,
		in: make(chan datagram, 64), closed: make(chan struct{}), rd: deadline.New(), wd: deadline.New()}
	select {
	case d.accept <- c:
		return c
	default:
		return nil
	}
}

// handshakeComplete marks c established. A pending connection takes over
// the address and the association it replaces is closed (RFC 6347
// §4.2.8: abandon it after a correct Finished).
func (d *demux) handshakeComplete(c *packetConn) {
	c.established.Store(true)
	d.mu.Lock()
	if d.pending[c.key] != c {
		d.mu.Unlock()
		return
	}
	delete(d.pending, c.key)
	old := d.addrs[c.key]
	d.addrs[c.key] = c
	d.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// newCID returns a fresh random CID routed to c.
func (d *demux) newCID(c *packetConn) []byte {
	cid := make([]byte, d.cidLen)
	d.mu.Lock()
	defer d.mu.Unlock()
	for {
		_, _ = rand.Read(cid)
		if d.cids[string(cid)] == nil {
			break
		}
	}
	if !c.isClosed() {
		d.cids[string(cid)] = c
		c.cids = append(c.cids, string(cid))
	}
	return bytes.Clone(cid)
}

// moved records that established c now sends to addr: pion moved the
// association after an authenticated CID record from there (RFC 9146
// §6). The old address is freed, so a new client there starts a fresh
// association instead of a takeover of this one.
func (d *demux) moved(c *packetConn, addr net.Addr) {
	key := addr.String()
	d.mu.Lock()
	defer d.mu.Unlock()
	if key == c.key || c.isClosed() || d.addrs[c.key] != c || d.addrs[key] != nil {
		return
	}
	delete(d.addrs, c.key)
	d.addrs[key] = c
	c.key = key
}

// closeLocked closes c and drops its routes. d.mu is held.
func (d *demux) closeLocked(c *packetConn) {
	c.closeOnce.Do(func() { close(c.closed) })
	if d.addrs[c.key] == c {
		delete(d.addrs, c.key)
	}
	if d.pending[c.key] == c {
		delete(d.pending, c.key)
	}
	for _, id := range c.cids {
		if d.cids[id] == c {
			delete(d.cids, id)
		}
	}
}

// classify looks at the first record: whether it is epoch 0, whether it
// is a handshake record and, for the first fragment of a ClientHello, the
// client random.
func classify(b []byte) (epoch0, isHandshake bool, random []byte) {
	recs, _ := recordlayer.UnpackDatagram(b, recordlayer.UnpackDatagramConfig{})
	if len(recs) == 0 {
		return false, false, nil
	}
	r, err := recordlayer.ParseRecord(recs[0], 0)
	if err != nil || r.IsUnified() || r.Epoch() != 0 {
		return false, err == nil && r.ContentType() == protocol.ContentTypeHandshake, nil
	}
	if r.ContentType() != protocol.ContentTypeHandshake {
		return true, false, nil
	}
	var h handshake.Header
	payload := r.Payload()
	at := handshake.HeaderLength + 2 // the random follows client_version
	if h.Unmarshal(payload) != nil || h.Type != handshake.TypeClientHello || h.FragmentOffset != 0 ||
		len(payload) < at+handshake.RandomLength {
		return true, true, nil
	}
	return true, true, payload[at : at+handshake.RandomLength]
}

// connectionID is the CID of the first record that carries one (pion's
// cidDatagramRouter).
func connectionID(b []byte, n int) []byte {
	recs, _ := recordlayer.UnpackDatagram(b, recordlayer.UnpackDatagramConfig{CIDLength: n, CIDRequired: true})
	for _, rec := range recs {
		if r, err := recordlayer.ParseRecord(rec, n); err == nil && len(r.ConnectionID()) > 0 {
			return r.ConnectionID()
		}
	}
	return nil
}

type datagram struct {
	b    []byte
	from net.Addr
}

// packetConn is one association's view of the socket.
type packetConn struct {
	d           *demux
	key         string // address route, guarded by d.mu
	raddr       net.Addr
	random      string   // ClientHello random of a pending connection
	cids        []string // guarded by d.mu
	established atomic.Bool

	in        chan datagram
	closed    chan struct{}
	closeOnce sync.Once
	rd, wd    *deadline.Deadline
}

func (c *packetConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *packetConn) deliver(b []byte, from net.Addr) {
	select {
	case c.in <- datagram{bytes.Clone(b), from}:
	default: // ponytail: full queue drops the datagram, as UDP would
	}
}

func (c *packetConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case dg := <-c.in:
		if len(p) < len(dg.b) {
			return 0, nil, errors.New("dtlscoap: buffer too small")
		}
		return copy(p, dg.b), dg.from, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-c.rd.Done():
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *packetConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	case <-c.wd.Done():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	if c.established.Load() {
		c.d.moved(c, addr)
	}
	return c.d.pc.WriteTo(p, addr)
}

func (c *packetConn) Close() error {
	c.d.mu.Lock()
	c.d.closeLocked(c)
	c.d.mu.Unlock()
	return nil
}

func (c *packetConn) LocalAddr() net.Addr { return c.d.pc.LocalAddr() }

func (c *packetConn) SetDeadline(t time.Time) error {
	c.rd.Set(t)
	c.wd.Set(t)
	return nil
}

func (c *packetConn) SetReadDeadline(t time.Time) error  { c.rd.Set(t); return nil }
func (c *packetConn) SetWriteDeadline(t time.Time) error { c.wd.Set(t); return nil }
