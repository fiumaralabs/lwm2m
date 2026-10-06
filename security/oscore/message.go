package oscore

import (
	"bytes"
	"sort"
	"sync"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Option numbers this package needs beyond go-coap's list.
const (
	OptionOSCORE     message.OptionID = 9   // RFC 8613 §2
	OptionHopLimit   message.OptionID = 16  // RFC 8768, Class U
	OptionEcho       message.OptionID = 252 // RFC 9175, Class E
	OptionRequestTag message.OptionID = 292 // RFC 9175, Class E
)

// classU reports the options that stay Outer only (Figure 5). Observe is
// both Inner and Outer and handled separately; every other option,
// including unknown ones, is Class E (§4.1).
func classU(id message.OptionID) bool {
	switch id {
	case message.URIHost, message.URIPort, OptionOSCORE, message.ProxyURI, message.ProxyScheme, OptionHopLimit:
		return true
	}
	return false
}

// Header is the decompressed COSE header from the OSCORE option (§6.1).
type Header struct {
	PIV        []byte // nil: absent
	KID        []byte // nil: absent
	KIDContext []byte // nil: absent
}

// ParseHeader decodes an OSCORE option value. A server uses KID and
// KIDContext to find the Recipient Context (§8.2 step 2).
func ParseHeader(v []byte) (Header, error) {
	var h Header
	if len(v) == 0 {
		return h, nil
	}
	flags := v[0]
	if flags&0xe0 != 0 {
		return h, ErrDecode // reserved bits
	}
	n := int(flags & 0x07)
	if n > 5 {
		return h, ErrDecode
	}
	v = v[1:]
	if len(v) < n {
		return h, ErrDecode
	}
	if n > 0 {
		h.PIV = append([]byte(nil), v[:n]...)
	}
	v = v[n:]
	if flags&0x10 != 0 {
		if len(v) < 1 || len(v) < 1+int(v[0]) {
			return h, ErrDecode
		}
		h.KIDContext = append([]byte{}, v[1:1+int(v[0])]...)
		v = v[1+int(v[0]):]
	}
	if flags&0x08 != 0 {
		h.KID = append([]byte{}, v...)
	} else if len(v) > 0 {
		return h, ErrDecode
	}
	return h, nil
}

func (h Header) encode() []byte {
	if h.PIV == nil && h.KID == nil && h.KIDContext == nil {
		return []byte{} // all flags zero: empty value
	}
	b := []byte{byte(len(h.PIV))}
	b = append(b, h.PIV...)
	if h.KIDContext != nil {
		b[0] |= 0x10
		b = append(b, byte(len(h.KIDContext)))
		b = append(b, h.KIDContext...)
	}
	if h.KID != nil {
		b[0] |= 0x08
		b = append(b, h.KID...)
	}
	return b
}

// Exchange binds a response to its request (§7.1, §8): the request's kid
// and Partial IV enter the AAD and its nonce may be reused. For an Observe
// registration it also holds the Notification Number (§4.1.3.5.2).
type Exchange struct {
	KID, PIV []byte
	nonce    []byte
	observe  bool

	mu        sync.Mutex
	noPIVUsed bool
	notif     uint64
	haveNotif bool
	sentFirst bool
}

// RequestPIV is the request's Partial IV as a number.
func (x *Exchange) RequestPIV() uint64 { return decodePIV(x.PIV) }

// Observe reports an Observe registration.
func (x *Exchange) Observe() bool { return x.observe }

// split separates Inner (Class E) and Outer options. Observe goes to both.
func split(opts message.Options) (inner, outer message.Options, err error) {
	for _, o := range opts {
		switch {
		case o.ID == OptionOSCORE:
			return nil, nil, ErrNested
		case o.ID == message.Observe:
			inner = append(inner, o)
			outer = append(outer, o)
		case classU(o.ID):
			outer = append(outer, o)
		default:
			inner = append(inner, o)
		}
	}
	return inner, outer, nil
}

func plaintext(code codes.Code, inner message.Options, payload []byte) ([]byte, error) {
	n, err := inner.Marshal(nil)
	if err != nil && n < 0 {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := inner.Marshal(buf); err != nil {
		return nil, err
	}
	pt := append([]byte{byte(code)}, buf...)
	if len(payload) > 0 {
		pt = append(append(pt, 0xff), payload...)
	}
	return pt, nil
}

func parsePlaintext(pt []byte) (codes.Code, message.Options, []byte, error) {
	if len(pt) < 1 {
		return 0, nil, nil, ErrDecode
	}
	opts := make(message.Options, 0, 64)
	n, err := opts.Unmarshal(pt[1:], map[message.OptionID]message.OptionDef{})
	if err != nil {
		return 0, nil, nil, ErrDecode
	}
	var payload []byte
	if rest := pt[1+n:]; len(rest) > 0 {
		payload = rest
	}
	return codes.Code(pt[0]), opts, payload, nil
}

func sorted(o message.Options) message.Options {
	sort.SliceStable(o, func(i, j int) bool { return o[i].ID < o[j].ID })
	return o
}

func get(opts message.Options, id message.OptionID) (message.Option, bool) {
	for _, o := range opts {
		if o.ID == id {
			return o, true
		}
	}
	return message.Option{}, false
}

// ProtectRequest turns a CoAP request into an OSCORE request (§8.1). The
// returned Exchange verifies the response(s).
func (c *Context) ProtectRequest(m message.Message) (message.Message, *Exchange, error) {
	inner, outer, err := split(m.Options)
	if err != nil {
		return m, nil, err
	}
	pt, err := plaintext(m.Code, inner, m.Payload)
	if err != nil {
		return m, nil, err
	}
	piv, err := c.nextPIV()
	if err != nil {
		return m, nil, err
	}
	kid := append([]byte{}, c.p.SenderID...)
	nonce := c.Nonce(c.p.SenderID, piv)
	ct := c.sender.Seal(nil, nonce, pt, c.AAD(kid, piv))
	h := Header{PIV: piv, KID: kid}
	if c.p.SendKIDContext && c.p.IDContext != nil {
		h.KIDContext = c.p.IDContext
	}
	x := &Exchange{KID: kid, PIV: piv, nonce: nonce}
	out := m
	out.Code = codes.POST // §4.2
	if o, ok := get(inner, message.Observe); ok {
		out.Code = codes.Code(5) // FETCH (§4.1.3.5)
		x.observe = decodePIV(o.Value) == 0
	}
	out.Options = sorted(append(outer, message.Option{ID: OptionOSCORE, Value: h.encode()}))
	out.Payload = ct
	return out, x, nil
}

// optionValue returns the OSCORE option of m.
func optionValue(m message.Message) ([]byte, error) {
	o, ok := get(m.Options, OptionOSCORE)
	if !ok {
		return nil, ErrNotOSCORE
	}
	return o.Value, nil
}

// rebuild writes the decrypted Code, options and payload into a message
// carrying m's Outer Class U options and header fields (§8.2 step 7).
func rebuild(m message.Message, code codes.Code, inner message.Options, payload []byte) message.Message {
	out := m
	out.Code = code
	opts := message.Options{}
	for _, o := range m.Options {
		if classU(o.ID) && o.ID != OptionOSCORE {
			opts = append(opts, o)
		}
	}
	out.Options = sorted(append(opts, inner...))
	out.Payload = payload
	return out
}

// UnprotectRequest verifies an OSCORE request (§8.2). The caller found
// this context from the Header's kid (and kid context).
func (c *Context) UnprotectRequest(m message.Message) (message.Message, *Exchange, error) {
	v, err := optionValue(m)
	if err != nil {
		return m, nil, err
	}
	h, err := ParseHeader(v)
	if err != nil {
		return m, nil, err
	}
	if h.PIV == nil || h.KID == nil {
		return m, nil, ErrDecode // both SHALL be present in requests (§5)
	}
	if !bytes.Equal(h.KID, c.p.RecipientID) || (h.KIDContext != nil && !bytes.Equal(h.KIDContext, c.p.IDContext)) {
		return m, nil, ErrNoContext
	}
	n := decodePIV(h.PIV)
	if n > MaxSequence {
		return m, nil, ErrDecode
	}
	nonce := c.Nonce(c.p.RecipientID, h.PIV)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.window.seen(n) {
		return m, nil, ErrReplay
	}
	pt, err := c.recipient.Open(nil, nonce, m.Payload, c.AAD(h.KID, h.PIV))
	if err != nil {
		return m, nil, ErrDecrypt
	}
	code, inner, payload, err := parsePlaintext(pt)
	if err != nil {
		return m, nil, err
	}
	c.window.accept(n)
	x := &Exchange{KID: h.KID, PIV: h.PIV, nonce: nonce}
	if o, ok := get(inner, message.Observe); ok {
		x.observe = decodePIV(o.Value) == 0
	}
	return rebuild(m, code, inner, payload), x, nil
}

// ProtectResponse turns a CoAP response to the request of x into an
// OSCORE response (§8.3). newPIV uses a fresh Partial IV instead of the
// request nonce; notifications after the first always get one
// (§4.1.3.5.2, §8.3.1), and so does a response when the server cannot
// perform replay protection (Appendix B.1.2).
func (c *Context) ProtectResponse(m message.Message, x *Exchange, newPIV bool) (message.Message, error) {
	return c.protectResponse(m, x, newPIV, nil)
}

// protectResponse is ProtectResponse with an optional 'kid context' in
// the response (Appendix B.2 response #1).
func (c *Context) protectResponse(m message.Message, x *Exchange, newPIV bool, kidContext []byte) (message.Message, error) {
	inner, outer, err := split(m.Options)
	if err != nil {
		return m, err
	}
	_, notification := get(inner, message.Observe)
	if notification {
		for i := range inner {
			if inner[i].ID == message.Observe {
				inner[i].Value = nil // Inner Observe is empty in notifications
			}
		}
		x.mu.Lock()
		if x.sentFirst {
			newPIV = true
		}
		x.sentFirst = true
		x.mu.Unlock()
	}
	pt, err := plaintext(m.Code, inner, m.Payload)
	if err != nil {
		return m, err
	}
	h := Header{KIDContext: kidContext}
	nonce := x.nonce
	if newPIV {
		if h.PIV, err = c.nextPIV(); err != nil {
			return m, err
		}
		nonce = c.Nonce(c.p.SenderID, h.PIV)
	}
	out := m
	out.Code = codes.Changed
	if notification {
		out.Code = codes.Content
	}
	out.Options = sorted(append(outer, message.Option{ID: OptionOSCORE, Value: h.encode()}))
	out.Payload = c.sender.Seal(nil, nonce, pt, c.AAD(x.KID, x.PIV))
	return out, nil
}

// UnprotectResponse verifies an OSCORE response to the request of x
// (§8.4), with the replay protection of notifications (§7.4.1). The
// decrypted notification's Observe value is set to the three least
// significant bytes of its Partial IV (§4.1.3.5.2), 0 without one.
func (c *Context) UnprotectResponse(m message.Message, x *Exchange) (message.Message, error) {
	v, err := optionValue(m)
	if err != nil {
		return m, err
	}
	h, err := ParseHeader(v)
	if err != nil {
		return m, err
	}
	nonce := x.nonce
	if h.PIV != nil {
		nonce = c.Nonce(c.p.RecipientID, h.PIV)
	}
	pt, err := c.recipient.Open(nil, nonce, m.Payload, c.AAD(x.KID, x.PIV))
	if err != nil {
		return m, ErrDecrypt
	}
	code, inner, payload, err := parsePlaintext(pt)
	if err != nil {
		return m, err
	}
	if _, ok := get(inner, message.Observe); ok {
		if !x.observe {
			return m, ErrDecode // Inner Observe on a non-Observe request (§4.1.3.5.2)
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		var seq uint32
		if h.PIV == nil {
			if x.noPIVUsed {
				return m, ErrReplay // at most one notification without Partial IV
			}
			x.noPIVUsed = true
		} else {
			n := decodePIV(h.PIV)
			if x.haveNotif && n <= x.notif {
				return m, ErrReplay
			}
			x.notif, x.haveNotif = n, true
			seq = uint32(n & 0xffffff)
		}
		for i := range inner {
			if inner[i].ID == message.Observe {
				inner[i].Value = encodeObserve(seq)
			}
		}
	}
	return rebuild(m, code, inner, payload), nil
}

func encodeObserve(v uint32) []byte {
	switch {
	case v == 0:
		return nil
	case v < 1<<8:
		return []byte{byte(v)}
	case v < 1<<16:
		return []byte{byte(v >> 8), byte(v)}
	}
	return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
}
