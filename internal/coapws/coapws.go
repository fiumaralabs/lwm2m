// Package coapws adapts a WebSocket carrying CoAP (RFC 8323 §4) to the
// byte stream of CoAP over TCP (RFC 8323 §3), so go-coap's TCP stack (CSM,
// Ping/Pong, observe, blockwise) runs unchanged over it. Each WebSocket
// binary message is one CoAP message whose first byte has a zero Len
// nibble and no extended length (RFC 8323 §4.2); the TCP framing adds the
// length the stream needs.
package coapws

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Subprotocol is the WebSocket subprotocol for CoAP (RFC 8323 §4.1, §11.4).
const Subprotocol = "coap"

// MaxMessage bounds one inbound WebSocket message.
// ponytail: fixed 1 MiB; larger bodies go blockwise (RFC 8323 §6).
const MaxMessage = 1 << 20

// Conn is a net.Conn speaking the RFC 8323 TCP framing over ws.
type Conn struct {
	ws    *websocket.Conn
	rbuf  []byte // TCP-framed bytes not yet read
	wmu   sync.Mutex
	wbuf  []byte // TCP-framed bytes not yet sent
	close sync.Once
}

// NewConn wraps ws, whose handshake has negotiated Subprotocol.
func NewConn(ws *websocket.Conn) *Conn {
	ws.SetReadLimit(MaxMessage)
	return &Conn{ws: ws}
}

// Read returns the TCP framing of the next WebSocket message.
func (c *Conn) Read(b []byte) (int, error) {
	for len(c.rbuf) == 0 {
		typ, m, err := c.ws.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		if typ != websocket.BinaryMessage {
			return 0, errors.New("coapws: CoAP needs binary WebSocket messages (RFC 8323 §4.2)")
		}
		if c.rbuf, err = ToTCP(m); err != nil {
			return 0, err
		}
	}
	n := copy(b, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

// Write sends every complete TCP-framed message in b as one WebSocket
// binary message.
func (c *Conn) Write(b []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.wbuf = append(c.wbuf, b...)
	for {
		m, n, err := FromTCP(c.wbuf)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return len(b), nil
		}
		if err := c.ws.WriteMessage(websocket.BinaryMessage, m); err != nil {
			return 0, err
		}
		c.wbuf = c.wbuf[n:]
	}
}

// Close closes the WebSocket.
func (c *Conn) Close() error {
	err := net.ErrClosed
	c.close.Do(func() {
		_ = c.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		err = c.ws.Close()
	})
	return err
}

func (c *Conn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error {
	return errors.Join(c.ws.SetReadDeadline(t), c.ws.SetWriteDeadline(t))
}
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// ToTCP converts a WebSocket CoAP message (RFC 8323 §4.2) to TCP framing
// (§3.2): the Len nibble and extended length count options and payload.
func ToTCP(m []byte) ([]byte, error) {
	if len(m) < 2 || m[0]>>4 != 0 {
		return nil, fmt.Errorf("coapws: malformed message header % x", m[:min(len(m), 2)])
	}
	tkl := int(m[0] & 0xf)
	l := len(m) - 2 - tkl
	if tkl > 8 || l < 0 {
		return nil, fmt.Errorf("coapws: bad token length %d", tkl)
	}
	var h []byte
	switch {
	case l < 13:
		h = []byte{byte(l<<4) | byte(tkl)}
	case l < 269:
		h = []byte{13<<4 | byte(tkl), byte(l - 13)}
	case l < 65805:
		v := l - 269
		h = []byte{14<<4 | byte(tkl), byte(v >> 8), byte(v)}
	default:
		v := l - 65805
		h = []byte{15<<4 | byte(tkl), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	return append(h, m[1:]...), nil
}

// FromTCP takes the first complete TCP-framed message off b and returns
// it in WebSocket framing with the bytes it used; n is 0 while b holds
// only part of a message.
func FromTCP(b []byte) (m []byte, n int, err error) {
	if len(b) == 0 {
		return nil, 0, nil
	}
	ext := [16]int{13: 1, 14: 2, 15: 4}[b[0]>>4]
	if len(b) < 1+ext {
		return nil, 0, nil
	}
	l := int(b[0] >> 4)
	switch ext {
	case 1:
		l = int(b[1]) + 13
	case 2:
		l = int(b[1])<<8 | int(b[2]) + 269
	case 4:
		l = int(b[1])<<24 | int(b[2])<<16 | int(b[3])<<8 | int(b[4]) + 65805
	}
	tkl := int(b[0] & 0xf)
	if tkl > 8 {
		return nil, 0, fmt.Errorf("coapws: bad token length %d", tkl)
	}
	n = 1 + ext + 1 + tkl + l
	if len(b) < n {
		return nil, 0, nil
	}
	return append([]byte{byte(tkl)}, b[1+ext:n]...), n, nil
}
