// Package smsbinding is LwM2M binding S: CoAP over SMS (T §6.8.3) through
// a pluggable SMSC, and the SMS trigger (T §6.6) that wakes a client
// registered on another binding.
package smsbinding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// SMS is one short message with 8-bit data coding (3GPP 23.038).
type SMS struct {
	MSISDN string // TP-DA when sent, TP-OA when received
	UDH    []byte // user data header information elements, without the UDHL byte
	Data   []byte // user data after the header
	PID    byte   // TP-PID; 0 is the default
	DCS    byte   // TP-DCS; 0 is sent as DCS8Bit
}

// DCS8Bit is TP-DCS 0000 0100: 8-bit data, no message class (3GPP 23.038
// §4). CoAP over SMS uses 8-bit encoding (T §6.8.3); 0 would be the GSM
// 7-bit alphabet.
const DCS8Bit = 0x04

// MaxUserData is the user data size of one SMS, UDHL and UDH included.
const MaxUserData = 140

// SMSC submits mobile-terminated short messages (an SMPP or HTTP SMS
// gateway). Mobile-originated ones are handed to Binding.Deliver.
type SMSC interface {
	Submit(ctx context.Context, m SMS) error
}

// Mode is the /0/x/6 SMS Security Mode (Core E.1 res 6).
type Mode uint8

const (
	ModeDTLS   Mode = 1 // device end-point, DTLS (T §5.3.2.1)
	ModeSecure Mode = 2 // Secure Packet Structure, smartcard end-point (T §5.3.2.1.2)
	ModeNoSec  Mode = 3
)

// ParseMode validates a /0/x/6 value: 1-3, or 204-255 (proprietary).
// 4 is reserved and 0, 5-203 are undefined.
func ParseMode(v int64) (Mode, error) {
	if v >= 1 && v <= 3 || v >= 204 && v <= 255 {
		return Mode(v), nil
	}
	return 0, fmt.Errorf("smsbinding: SMS security mode %d is reserved or undefined", v)
}

// PayloadBudget is the LwM2M payload one SMS carries in DTLS mode
// (T §5.3.2.1.1): 140 bytes less 29 of DTLS and a 4-byte CoAP header,
// 8 more for a token.
func PayloadBudget(withToken bool) int {
	if withToken {
		return MaxUserData - 29 - 4 - 8 // 99
	}
	return MaxUserData - 29 - 4 // 107
}

// Security protects the SMS channel (SMS Secured mode, T §5.3.2): DTLS
// for a device end-point or 3GPP 31.115 Secured Packets for a smartcard
// (SecuredPacket). Seal returns the SMS to send (a non-empty MSISDN
// overrides the TP-DA). Open gets the inbound SMS, reassembled, with the
// TP-OA as received and the first part's UDH, and must fail for anything
// not protected with the expected /0/x/7 parameters and /0/x/8 keys.
type Security interface {
	Seal(msisdn string, coap []byte) (SMS, error)
	Open(m SMS) ([]byte, error)
}

// Config configures a Binding.
type Config struct {
	Server *server.Server
	SMSC   SMSC
	// Security secures CoAP over SMS. nil is NoSec: then the binding only
	// sends triggers and drops every inbound SMS, unless Debug is set
	// (T §5.3: NoSec only for debugging or for triggering).
	Security Security
	Debug    bool
	// OSCORE, if set, protects LwM2M over SMS for endpoints with an OSCORE
	// context (/0/x/17), as on UDP (T §5.4.1): with Security nil (SMS
	// NoSec plus /0/x/17) OSCORE is the channel's only protection and
	// unprotected requests are dropped unless Debug (T §5.3.1); with
	// Security set the messages are protected by both, and an unprotected
	// Register, Update or De-register for an OSCORE endpoint is refused.
	// Triggers to a registered OSCORE endpoint are protected too.
	OSCORE *server.OSCORE
	// Allowed, if set, filters inbound SMS by originating MSISDN; others
	// are silently ignored.
	Allowed func(msisdn string) bool
	// ResponseTimeout bounds the wait for a response. CoAP retransmission
	// is off on SMS (T §6.8.3); 0 waits for the request context.
	ResponseTimeout time.Duration
}

// Binding is the SMS binding of one server.
type Binding struct {
	cfg   Config
	mu    sync.Mutex
	conns map[string]*coapwire.Conn
	ref   uint8
	parts map[string][]*SMS // MSISDN/ref -> concatenated parts
}

func New(cfg Config) *Binding {
	return &Binding{cfg: cfg, conns: map[string]*coapwire.Conn{}, parts: map[string][]*SMS{}}
}

var ErrMSISDN = errors.New("smsbinding: invalid MSISDN")

// NormalizeMSISDN strips "+", spaces and dashes and checks that 1-15
// digits remain (E.164, 3GPP 23.003).
func NormalizeMSISDN(s string) (string, error) {
	s = strings.NewReplacer("+", "", " ", "", "-", "").Replace(s)
	if len(s) == 0 || len(s) > 15 || strings.Trim(s, "0123456789") != "" {
		return "", fmt.Errorf("%w: %q", ErrMSISDN, s)
	}
	return s, nil
}

// Peer returns the server.Peer of msisdn.
func (b *Binding) Peer(msisdn string) (*coapwire.Conn, error) {
	n, err := NormalizeMSISDN(msisdn)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.conns[n]; ok {
		return c, nil
	}
	addr := coapwire.Addr{Net: "sms", ID: n}
	c := coapwire.NewConn(coapwire.Config{
		Server:     b.cfg.Server,
		Binding:    "S",
		Identity:   server.Identity{Mode: server.ModeNoSec, Addr: addr.String()},
		Addr:       addr,
		AckTimeout: b.cfg.ResponseTimeout,
		Send:       func(ctx context.Context, p []byte) error { return b.send(ctx, n, p) },
		OSCORE:     b.cfg.OSCORE,
		// SMS NoSec plus /0/x/17: the channel is protected with OSCORE (T §5.3.1).
		RequireOSCORE: b.cfg.OSCORE != nil && b.cfg.Security == nil && !b.cfg.Debug,
	})
	b.conns[n] = c
	return c, nil
}

func (b *Binding) send(ctx context.Context, msisdn string, coap []byte) error {
	if b.cfg.Security == nil && !b.cfg.Debug && b.cfg.OSCORE == nil {
		return errors.New("smsbinding: NoSec SMS is trigger-only (set Debug or OSCORE)")
	}
	m := SMS{Data: coap}
	if b.cfg.Security != nil {
		var err error
		if m, err = b.cfg.Security.Seal(msisdn, coap); err != nil {
			return err
		}
	}
	if m.MSISDN == "" {
		m.MSISDN = msisdn
	}
	return b.submit(ctx, m)
}

// submit sends m in one SMS, or concatenated SMS with the 8-bit
// reference IE (3GPP 23.040 §9.2.3.24.1) when it does not fit. A Command
// or Response Packet IE goes in the first part only (31.115 §4.3, §4.5).
func (b *Binding) submit(ctx context.Context, m SMS) error {
	if m.DCS == 0 {
		m.DCS = DCS8Bit
	}
	udh, data := m.UDH, m.Data
	if 1+len(udh)+len(data) <= MaxUserData || len(udh) == 0 && len(data) <= MaxUserData {
		return b.cfg.SMSC.Submit(ctx, m)
	}
	var rest []byte // UDH of later parts
	for ie := udh; len(ie) >= 2 && len(ie) >= 2+int(ie[1]); ie = ie[2+int(ie[1]):] {
		if ie[0] != IECommandPacket && ie[0] != IEResponsePacket {
			rest = append(rest, ie[:2+int(ie[1])]...)
		}
	}
	room := MaxUserData - 1 - len(udh) - 5
	n := (len(data) + room - 1) / room
	if n > 255 {
		return errors.New("smsbinding: message too long for concatenated SMS")
	}
	b.mu.Lock()
	b.ref++
	ref := b.ref
	b.mu.Unlock()
	for i := range n {
		part := data[i*room : min(len(data), (i+1)*room)]
		if i == 1 {
			udh = rest
		}
		h := append(append([]byte{}, udh...), 0x00, 0x03, ref, byte(n), byte(i+1))
		if err := b.cfg.SMSC.Submit(ctx, SMS{MSISDN: m.MSISDN, UDH: h, Data: part, PID: m.PID, DCS: m.DCS}); err != nil {
			return err
		}
	}
	return nil
}

// Deliver handles one mobile-originated SMS from the SMSC.
func (b *Binding) Deliver(ctx context.Context, m SMS) error {
	n, err := NormalizeMSISDN(m.MSISDN)
	if err != nil {
		return err
	}
	if b.cfg.Allowed != nil && !b.cfg.Allowed(n) {
		return nil // unknown sender: silently ignored (T §5.3)
	}
	m, done := b.reassemble(n, m)
	if !done {
		return nil
	}
	data := m.Data
	if b.cfg.Security == nil {
		if !b.cfg.Debug && b.cfg.OSCORE == nil {
			return nil // NoSec: inbound CoAP over SMS is for debugging only (T §5.3)
		}
	} else if data, err = b.cfg.Security.Open(m); err != nil {
		return nil // not correctly protected: discard, no reply (T §5.3.2)
	}
	c, err := b.Peer(n)
	if err != nil {
		return err
	}
	return c.Receive(ctx, data)
}

// reassemble joins concatenated SMS parts; the result keeps the first
// part's UDH.
// ponytail: incomplete sets stay buffered forever; add expiry if senders lose parts.
func (b *Binding) reassemble(msisdn string, m SMS) (SMS, bool) {
	for ie := m.UDH; len(ie) >= 2 && len(ie) >= 2+int(ie[1]); ie = ie[2+int(ie[1]):] {
		if ie[0] != 0x00 || ie[1] != 3 {
			continue
		}
		ref, total, seq := ie[2], int(ie[3]), int(ie[4])
		if total == 0 || seq == 0 || seq > total {
			return SMS{}, false
		}
		key := fmt.Sprintf("%s/%d", msisdn, ref)
		b.mu.Lock()
		defer b.mu.Unlock()
		ps := b.parts[key]
		if ps == nil {
			ps = make([]*SMS, total)
			b.parts[key] = ps
		}
		if len(ps) != total {
			return SMS{}, false
		}
		ps[seq-1] = &m
		var data [][]byte
		for _, p := range ps {
			if p == nil {
				return SMS{}, false
			}
			data = append(data, p.Data)
		}
		delete(b.parts, key)
		out := *ps[0]
		out.Data = bytes.Join(data, nil)
		return out, true
	}
	return m, true
}

// WAP Push framing of App. L: WDP port IE (destination 2948) and a WSP
// push header carrying X-WAP-Application-ID 0x9A
// (x-wap-application:lwm2m.dm).
var (
	wdpUDH    = []byte{0x05, 0x04, 0x0B, 0x84, 0xC0, 0x02}
	wspHeader = []byte{0x01, 0x06, 0x03, 0xC4, 0xAF, 0x9A}
)

const (
	PushPort     = 2948
	LwM2MAppID   = 0x9A
	pathUpdate   = 8 // /1/x/8 Registration Update Trigger
	pathBootstrp = 9 // /1/x/9 Bootstrap-Request Trigger
)

// TriggerMessage is the CoAP Execute of /1/{instance}/8, or /1/{instance}/9
// for a bootstrap trigger (T §6.6.1, §6.6.2), as a CON with token.
func TriggerMessage(mid uint16, token []byte, instance uint16, bootstrap bool) ([]byte, error) {
	r := pathUpdate
	if bootstrap {
		r = pathBootstrp
	}
	return coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: mid, Msg: server.Message{
		Code: codes.POST, Path: fmt.Sprintf("/1/%d/%d", instance, r), Token: token,
	}})
}

// WAPPush wraps a CoAP message as App. L: it returns the UDH IEs and the
// user data.
func WAPPush(coap []byte) (udh, data []byte) {
	return append([]byte{}, wdpUDH...), append(append([]byte{}, wspHeader...), coap...)
}

// ParseWAPPush returns the CoAP message of an LwM2M WAP Push: the WDP
// destination port must be 2948 and the application ID 0x9A.
func ParseWAPPush(m SMS) ([]byte, error) {
	port := -1
	for ie := m.UDH; len(ie) >= 2 && len(ie) >= 2+int(ie[1]); ie = ie[2+int(ie[1]):] {
		if ie[0] == 0x05 && ie[1] == 4 {
			port = int(ie[2])<<8 | int(ie[3])
		}
	}
	if port != PushPort {
		return nil, fmt.Errorf("smsbinding: WDP port %d, want %d", port, PushPort)
	}
	d := m.Data
	if len(d) < 3 || d[1] != 0x06 || len(d) < 3+int(d[2]) {
		return nil, errors.New("smsbinding: not a WSP push PDU")
	}
	hdr := d[3 : 3+int(d[2])]
	if !bytes.Contains(hdr[1:], []byte{0xAF, LwM2MAppID}) {
		return nil, errors.New("smsbinding: X-WAP-Application-ID is not lwm2m.dm")
	}
	return d[3+int(d[2]):], nil
}

// Trigger sends the SMS trigger to msisdn (T §6.6). The client never
// answers it, so Trigger returns once the SMSC accepted the message.
// NoSec triggers are allowed (T §5.3).
func (b *Binding) Trigger(ctx context.Context, msisdn string, instance uint16, bootstrap bool) error {
	n, err := NormalizeMSISDN(msisdn)
	if err != nil {
		return err
	}
	tok, err := message.GetToken()
	if err != nil {
		return err
	}
	c, _ := b.Peer(n)
	coap, err := TriggerMessage(c.NextMID(), tok[:4], instance, bootstrap)
	if err != nil {
		return err
	}
	if oc, ok := b.oscoreContext(n); ok {
		if coap, err = protectCoAP(oc, coap); err != nil {
			return err
		}
	}
	if b.cfg.Security != nil {
		m, err := b.cfg.Security.Seal(n, coap)
		if err != nil {
			return err
		}
		if len(m.UDH) > 0 { // a Secured Packet goes to the UICC by TAR, not by WAP Push
			if m.MSISDN == "" {
				m.MSISDN = n
			}
			return b.submit(ctx, m)
		}
		coap = m.Data
	}
	udh, data := WAPPush(coap)
	return b.submit(ctx, SMS{MSISDN: n, UDH: udh, Data: data})
}

// oscoreContext returns the OSCORE context of the endpoint registered with
// SMS number msisdn.
// ponytail: linear scan of the registrations per trigger; index by SMS number if fleets are large.
func (b *Binding) oscoreContext(msisdn string) (*oscore.Context, bool) {
	if b.cfg.OSCORE == nil {
		return nil, false
	}
	for _, reg := range b.cfg.Server.Store().All() {
		if n, err := NormalizeMSISDN(reg.SMS); err == nil && n == msisdn {
			if c, ok := b.cfg.OSCORE.Context(reg.Endpoint); ok {
				return c, true
			}
		}
	}
	return nil, false
}

// protectCoAP protects an encoded CoAP request with c (RFC 8613 §8.1).
func protectCoAP(c *oscore.Context, b []byte) ([]byte, error) {
	m, err := coapwire.UnmarshalCoAP(b)
	if err != nil {
		return nil, err
	}
	if m, _, err = c.ProtectRequest(m); err != nil {
		return nil, err
	}
	return coapwire.MarshalCoAP(m)
}

// ShouldTrigger reports whether the server may wake reg with an SMS
// trigger (T §6.6): the client gave an SMS number, its current binding is
// not S, and its /1/x/21 Trigger resource (read by the caller) is true.
func ShouldTrigger(reg *server.Registration, trigger bool) bool {
	return trigger && reg.SMS != "" && reg.Binding != "S"
}
