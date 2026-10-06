package smsbinding

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SMS Secured mode with a smartcard end-point (T §5.3.2.1.2, §5.3.2.1.3):
// every CoAP message, request or response, travels in a 3GPP TS 31.115
// §4.2 Command Packet (ETSI TS 102 225 §5.1) addressed to TAR B2 02 03,
// with a cryptographic checksum, ciphering and a strictly increasing
// counter.
//
// Command Packet in the SM user data (31.115 Tbl 1, 102 225 Tbl 1):
//
//	UDH IE 70 00 (CPI) | CPL(2) | CHL(1) | SPI(2) KIc KID TAR(3) | CNTR(5) PCNTR(1) CC | Secured Data+padding
//	                                                               '------------- ciphered ---------------'
//
// CC covers CPL..PCNTR and the padded Secured Data (102 225 Tbl 2 note 2,
// 31.115 §4.2); it is computed before ciphering.

const (
	IECommandPacket  = 0x70 // CPI as a UDH IE (31.115 §4.2)
	IEResponsePacket = 0x71 // RPI as a UDH IE (31.115 §4.4)
	PIDDataDownload  = 0x7F // TP-PID 01 111111, (U)SIM Data download (23.040 §9.2.3.9)
	DCSClass2        = 0xF6 // TP-DCS 1111 0110: 8-bit data, class 2 (23.038 §4)

	// SPI1 is cryptographic checksum (b2b1 10), ciphering (b3) and
	// "process if and only if counter value is higher" (b5b4 10).
	SPI1 = 0x16

	// KIc/KID algorithm codings (102 225 §5.1.2, §5.1.3.1); b8..b5 carry
	// the key index.
	AlgAES       = 0x02 // KIc: AES-CBC, KID: AES-CMAC
	AlgTripleDES = 0x09 // triple DES, outer CBC, three keys

	// DefaultTag is the Secured Data BER-TLV tag. T §5.3.2.1.3 says
	// "TBD : e.g. 0x05" (A-19).
	DefaultTag = 0x05

	maxCounter = 1<<40 - 1 // 5-octet CNTR; at the maximum it is blocked (102 225 §5.1.4)
)

// TARLwM2M is the Toolkit Application Reference of the LwM2M UICC
// application (ETSI TS 101 220 App. D), mandated by T §5.3.2.1.3.
var TARLwM2M = [3]byte{0xB2, 0x02, 0x03}

var (
	ErrSingleDES = errors.New("smsbinding: single and two-key DES must not be used (T §5.3.2.1.3)")
	ErrReplay    = errors.New("smsbinding: counter not higher than the last one received")
	errNotSecure = errors.New("smsbinding: not a correctly secured packet")
)

// PacketKeys are one client's SMS binding parameters (/0/x/7) and keys
// (/0/x/8).
type PacketKeys struct {
	KIc, KID byte // AlgAES or AlgTripleDES, ORed with the key index << 4
	// SPI2 is the PoR policy sent to the client (102 225 §5.1.1, second
	// octet; T: "PoR depends on LwM2M Server Policy"). 0 asks for none.
	SPI2         byte
	Cipher, Auth []byte // ciphering key (KIc) and checksum key (KID)
	CCLen        int    // AES-CMAC: 8 (default) or 4 octets; triple DES: 8
}

// SecuredPacket is a Security for ModeSecure. The same type serves either
// side, so a test can play the smartcard.
type SecuredPacket struct {
	Keys func(msisdn string) (PacketKeys, error)
	Tag  byte // Secured Data tag; 0 is DefaultTag
	// OnPoR receives each authenticated Response Packet (Proof of
	// Receipt) when SPI2 asks for one.
	OnPoR func(msisdn string, cntr uint64, status byte, data []byte)

	mu     sync.Mutex
	rx, tx map[string]uint64 // last counter received / sent, per MSISDN
	oa     map[string]string // last TP-OA as received
}

type suite struct {
	enc cipher.Block
	mac func([]byte) []byte
	cc  int
}

func algBlock(id byte, key []byte) (cipher.Block, error) {
	switch id & 0x0F {
	case AlgAES:
		return aes.NewCipher(key) // 16, 24 or 32 bytes (102 225 §5.1.2)
	case AlgTripleDES:
		if len(key) != 24 || bytes.Equal(key[:8], key[8:16]) || bytes.Equal(key[8:16], key[16:]) || bytes.Equal(key[:8], key[16:]) {
			return nil, errors.New("smsbinding: triple DES needs three different 8-byte keys")
		}
		return des.NewTripleDESCipher(key)
	case 0x01, 0x05, 0x0D: // DES, 2-key 3DES, DES
		return nil, ErrSingleDES
	}
	return nil, fmt.Errorf("smsbinding: algorithm %#02x is not AES or triple DES", id)
}

func (k PacketKeys) suite() (suite, error) {
	enc, err := algBlock(k.KIc, k.Cipher)
	if err != nil {
		return suite{}, err
	}
	auth, err := algBlock(k.KID, k.Auth)
	if err != nil {
		return suite{}, err
	}
	s := suite{enc: enc, cc: k.CCLen}
	if k.KID&0x0F == AlgAES {
		s.mac = func(m []byte) []byte { return cmac(auth, m) }
		if s.cc == 0 {
			s.cc = 8
		}
		if s.cc != 4 && s.cc != 8 {
			return suite{}, errors.New("smsbinding: AES CMAC length must be 4 or 8")
		}
	} else {
		s.mac = func(m []byte) []byte { return cbcMAC(auth, m) }
		if s.cc != 0 && s.cc != 8 {
			return suite{}, errors.New("smsbinding: triple DES checksum is 8 octets")
		}
		s.cc = 8
	}
	return s, nil
}

func (s *SecuredPacket) keys(msisdn string) (PacketKeys, suite, error) {
	k, err := s.Keys(msisdn)
	if err != nil {
		return k, suite{}, err
	}
	st, err := k.suite()
	return k, st, err
}

func (s *SecuredPacket) tag() byte {
	if s.Tag == 0 {
		return DefaultTag
	}
	return s.Tag
}

// Seal puts coap in a Command Packet for msisdn, sent as class 2 SMS with
// TP-PID 111111 to the TP-OA last received from that client.
func (s *SecuredPacket) Seal(msisdn string, coap []byte) (SMS, error) {
	return s.command(msisdn, coap, TARLwM2M)
}

func (s *SecuredPacket) command(msisdn string, coap []byte, tar [3]byte) (SMS, error) {
	k, st, err := s.keys(msisdn)
	if err != nil {
		return SMS{}, err
	}
	sd := berTLV(s.tag(), coap)
	bs := st.enc.BlockSize()
	pad := (bs - (6+st.cc+len(sd))%bs) % bs
	chl := 13 + st.cc
	cpl := 1 + chl + len(sd) + pad
	if cpl > 0xFFFF {
		return SMS{}, errors.New("smsbinding: message too long for a Command Packet")
	}
	s.mu.Lock()
	if s.tx == nil {
		s.tx, s.rx, s.oa = map[string]uint64{}, map[string]uint64{}, map[string]string{}
	}
	// ponytail: counters live in memory; seeding from the clock keeps them
	// rising across restarts while under 256 packets/s per client. Persist
	// tx/rx if that ceiling or a restart replay window matters.
	cntr := max(s.tx[msisdn]+1, uint64(time.Now().Unix())<<8)
	if cntr > maxCounter {
		s.mu.Unlock()
		return SMS{}, errors.New("smsbinding: counter blocked")
	}
	s.tx[msisdn] = cntr
	to := s.oa[msisdn]
	s.mu.Unlock()
	if to == "" {
		to = msisdn
	}
	h := []byte{byte(cpl >> 8), byte(cpl), byte(chl), SPI1, k.SPI2, k.KIc, k.KID, tar[0], tar[1], tar[2]}
	h = append(h, counter(cntr)...)
	h = append(h, byte(pad))
	body := append(sd, make([]byte, pad)...)
	cc := st.mac(append(append([]byte{}, h...), body...))[:st.cc]
	plain := append(append(append([]byte{}, h[10:]...), cc...), body...)
	return SMS{MSISDN: to, UDH: []byte{IECommandPacket, 0}, Data: append(h[:10], cbc(st.enc, plain, true)...),
		PID: PIDDataDownload, DCS: DCSClass2}, nil
}

// Open returns the CoAP message of a Command Packet, or handles a Response
// Packet (always returning an error, it carries no CoAP). Anything not
// protected with the client's parameters and keys, with another TAR or
// with a counter not above the last one is rejected.
func (s *SecuredPacket) Open(m SMS) ([]byte, error) {
	n, err := NormalizeMSISDN(m.MSISDN)
	if err != nil {
		return nil, err
	}
	udh, d := m.UDH, m.Data
	if len(udh) == 0 && bytes.HasPrefix(d, []byte{2, IEResponsePacket, 0}) {
		udh, d = d[1:3], d[3:] // PoR from a UICC that cannot set UDHI (31.115 §4.1)
	}
	k, st, err := s.keys(n)
	if err != nil {
		return nil, err
	}
	if hasIE(udh, IEResponsePacket) {
		return nil, s.openResponse(n, k, st, d)
	}
	if !hasIE(udh, IECommandPacket) || len(d) < 16 || int(binary.BigEndian.Uint16(d)) != len(d)-2 ||
		int(d[2]) != 13+st.cc || d[3]&0x1F != SPI1 || d[5] != k.KIc || d[6] != k.KID ||
		len(d[10:])%st.enc.BlockSize() != 0 || len(d[10:]) < 6+st.cc {
		return nil, errNotSecure
	}
	plain := cbc(st.enc, d[10:], false)
	body := plain[6+st.cc:]
	want := st.mac(append(append(append([]byte{}, d[:10]...), plain[:6]...), body...))[:st.cc]
	if subtle.ConstantTimeCompare(want, plain[6:6+st.cc]) != 1 {
		return nil, errNotSecure
	}
	if [3]byte(d[7:10]) != TARLwM2M {
		return nil, errors.New("smsbinding: TAR is not B2 02 03")
	}
	pcntr := int(plain[5])
	if pcntr > len(body) {
		return nil, errNotSecure
	}
	coap, err := parseBerTLV(s.tag(), body[:len(body)-pcntr])
	if err != nil {
		return nil, err
	}
	cntr := uint64(plain[0])<<32 | uint64(binary.BigEndian.Uint32(plain[1:5]))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rx == nil {
		s.tx, s.rx, s.oa = map[string]uint64{}, map[string]uint64{}, map[string]string{}
	}
	if cntr <= s.rx[n] {
		return nil, ErrReplay
	}
	s.rx[n], s.oa[n] = cntr, m.MSISDN
	return coap, nil
}

// ResponsePacket builds the Proof of Receipt for a Command Packet with
// counter cntr (102 225 §5.2, 31.115 §4.4), secured as k.SPI2 asks: the
// card side of a PoR exchange.
//
//	UDH IE 71 00 (RPI) | RPL(2) | RHL(1) | TAR(3) | CNTR(5) PCNTR(1) Status(1) CC | data+padding
func (s *SecuredPacket) ResponsePacket(msisdn string, cntr uint64, status byte, data []byte) (SMS, error) {
	k, st, err := s.keys(msisdn)
	if err != nil {
		return SMS{}, err
	}
	cc, ciphered, err := porSecurity(k, st)
	if err != nil {
		return SMS{}, err
	}
	pad := 0
	if ciphered {
		bs := st.enc.BlockSize()
		pad = (bs - (7+cc+len(data))%bs) % bs
	}
	rhl := 10 + cc
	rpl := 1 + rhl + len(data) + pad
	h := []byte{byte(rpl >> 8), byte(rpl), byte(rhl), TARLwM2M[0], TARLwM2M[1], TARLwM2M[2]}
	h = append(append(h, counter(cntr)...), byte(pad), status)
	body := append(append([]byte{}, data...), make([]byte, pad)...)
	var mac []byte
	if cc > 0 {
		mac = st.mac(append(append([]byte{2, IEResponsePacket, 0}, h...), body...))[:cc]
	}
	tail := append(append(append([]byte{}, h[6:]...), mac...), body...)
	if ciphered {
		tail = cbc(st.enc, tail, true)
	}
	return SMS{MSISDN: msisdn, UDH: []byte{IEResponsePacket, 0}, Data: append(h[:6], tail...),
		PID: PIDDataDownload, DCS: DCSClass2}, nil
}

// porSecurity applies the 102 225 §5.1.1 rules for response security: a
// PoR checksum must be the command's (CC), and ciphering needs both.
func porSecurity(k PacketKeys, st suite) (cc int, ciphered bool, err error) {
	switch k.SPI2 >> 2 & 3 {
	case 0:
	case 2:
		cc = st.cc
	default:
		return 0, false, errors.New("smsbinding: PoR checksum must match the command's")
	}
	return cc, k.SPI2&0x10 != 0, nil
}

func (s *SecuredPacket) openResponse(n string, k PacketKeys, st suite, d []byte) error {
	cc, ciphered, err := porSecurity(k, st)
	if err != nil || k.SPI2&3 == 0 || cc == 0 { // no PoR asked, or an unauthenticated one
		return errNotSecure
	}
	if len(d) < 13+cc || int(binary.BigEndian.Uint16(d)) != len(d)-2 || int(d[2]) != 10+cc {
		return errNotSecure
	}
	tail := d[6:]
	if ciphered {
		if len(tail)%st.enc.BlockSize() != 0 {
			return errNotSecure
		}
		tail = cbc(st.enc, tail, false)
	}
	body := tail[7+cc:]
	want := st.mac(append(append(append([]byte{2, IEResponsePacket, 0}, d[:6]...), tail[:7]...), body...))[:cc]
	if subtle.ConstantTimeCompare(want, tail[7:7+cc]) != 1 || [3]byte(d[3:6]) != TARLwM2M || int(tail[5]) > len(body) {
		return errNotSecure
	}
	cntr := uint64(tail[0])<<32 | uint64(binary.BigEndian.Uint32(tail[1:5]))
	s.mu.Lock()
	sent := s.tx[n]
	s.mu.Unlock()
	if cntr == 0 || cntr > sent {
		return errNotSecure
	}
	if s.OnPoR != nil {
		s.OnPoR(n, cntr, tail[6], body[:len(body)-int(tail[5])])
	}
	return errors.New("smsbinding: Response Packet handled")
}

func counter(c uint64) []byte {
	return []byte{byte(c >> 32), byte(c >> 24), byte(c >> 16), byte(c >> 8), byte(c)}
}

func hasIE(udh []byte, iei byte) bool {
	for ie := udh; len(ie) >= 2 && len(ie) >= 2+int(ie[1]); ie = ie[2+int(ie[1]):] {
		if ie[0] == iei && ie[1] == 0 {
			return true
		}
	}
	return false
}

// berTLV codes a one-byte tag and BER length (ETSI TS 101 220 §7.1.2).
func berTLV(tag byte, v []byte) []byte {
	out := []byte{tag}
	switch n := len(v); {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 0x100:
		out = append(out, 0x81, byte(n))
	default:
		out = append(out, 0x82, byte(n>>8), byte(n))
	}
	return append(out, v...)
}

func parseBerTLV(tag byte, b []byte) ([]byte, error) {
	if len(b) < 2 || b[0] != tag {
		return nil, fmt.Errorf("smsbinding: Secured Data tag is not %#02x", tag)
	}
	n, off := int(b[1]), 2
	switch b[1] {
	case 0x81:
		if len(b) < 3 {
			return nil, errNotSecure
		}
		n, off = int(b[2]), 3
	case 0x82:
		if len(b) < 4 {
			return nil, errNotSecure
		}
		n, off = int(binary.BigEndian.Uint16(b[2:])), 4
	default:
		if n >= 0x80 {
			return nil, errNotSecure
		}
	}
	if len(b) != off+n {
		return nil, errors.New("smsbinding: Secured Data length mismatch")
	}
	return b[off:], nil
}

// cbc ciphers with a zero initial chaining value (102 225 §5.1.2).
func cbc(b cipher.Block, in []byte, encrypt bool) []byte {
	out := make([]byte, len(in))
	iv := make([]byte, b.BlockSize())
	if encrypt {
		cipher.NewCBCEncrypter(b, iv).CryptBlocks(out, in)
	} else {
		cipher.NewCBCDecrypter(b, iv).CryptBlocks(out, in)
	}
	return out
}

// cbcMAC is the triple DES checksum: CBC, zero ICV, '00' padding not sent
// (102 225 §5.1.3.1); the CC is the last block.
func cbcMAC(b cipher.Block, m []byte) []byte {
	bs := b.BlockSize()
	in := append(append([]byte{}, m...), make([]byte, (bs-len(m)%bs)%bs)...)
	if len(in) == 0 {
		in = make([]byte, bs)
	}
	out := cbc(b, in, true)
	return out[len(out)-bs:]
}

// cmac is AES-CMAC (RFC 4493, NIST SP 800-38B).
func cmac(b cipher.Block, m []byte) []byte {
	const bs = 16
	dbl := func(in []byte) []byte {
		out := make([]byte, bs)
		for i := range bs {
			out[i] = in[i] << 1
			if i+1 < bs {
				out[i] |= in[i+1] >> 7
			}
		}
		if in[0]&0x80 != 0 {
			out[bs-1] ^= 0x87
		}
		return out
	}
	l := make([]byte, bs)
	b.Encrypt(l, l)
	k1 := dbl(l)
	k2 := dbl(k1)
	n := (len(m) + bs - 1) / bs
	last := make([]byte, bs)
	if n > 0 && len(m)%bs == 0 {
		subtle.XORBytes(last, m[(n-1)*bs:], k1)
	} else {
		if n == 0 {
			n = 1
		}
		rest := m[(n-1)*bs:]
		copy(last, rest)
		last[len(rest)] = 0x80
		subtle.XORBytes(last, last, k2)
	}
	x := make([]byte, bs)
	for i := range n - 1 {
		subtle.XORBytes(x, x, m[i*bs:(i+1)*bs])
		b.Encrypt(x, x)
	}
	subtle.XORBytes(x, x, last)
	b.Encrypt(x, x)
	return x
}
