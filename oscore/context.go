// Package oscore implements Object Security for Constrained RESTful
// Environments (RFC 8613): security context derivation (§3), message
// protection and verification (§4, §5, §8), header compression (§6) and
// replay protection (§7). LwM2M uses it as a security layer over CoAP
// (T §5.4).
package oscore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/pion/dtls/v3/pkg/crypto/ccm"
)

// COSE AEAD algorithms (RFC 8152 Tbl 10, 9). AES-CCM-16-64-128 is the
// mandatory default (RFC 8613 §3.2, LwM2M T §5.4.2).
const (
	AESCCM16_64_128  = 10
	AESCCM16_64_256  = 11
	AESCCM64_64_128  = 12
	AESCCM64_64_256  = 13
	AESCCM16_128_128 = 30
	AESCCM16_128_256 = 31
	AESCCM64_128_128 = 32
	AESCCM64_128_256 = 33
	A128GCM          = 1
	A192GCM          = 2
	A256GCM          = 3
)

// HKDF algorithms. COSE names them by HKDF (-10 SHA-256, -11 SHA-512);
// LwM2M /21/x/4 carries an HMAC algorithm of RFC 8152 Tbl 7 (5 HMAC
// 256/256, 6 HMAC 384/384, 7 HMAC 512/512). Both encodings are accepted.
const (
	HKDFSHA256 = -10
	HKDFSHA512 = -11
	HMAC256    = 5
	HMAC384    = 6
	HMAC512    = 7
)

type aeadAlg struct{ key, nonce, tag int }

var aeads = map[int]aeadAlg{
	AESCCM16_64_128: {16, 13, 8}, AESCCM16_64_256: {32, 13, 8},
	AESCCM64_64_128: {16, 7, 8}, AESCCM64_64_256: {32, 7, 8},
	AESCCM16_128_128: {16, 13, 16}, AESCCM16_128_256: {32, 13, 16},
	AESCCM64_128_128: {16, 7, 16}, AESCCM64_128_256: {32, 7, 16},
	A128GCM: {16, 12, 16}, A192GCM: {24, 12, 16}, A256GCM: {32, 12, 16},
}

func (a aeadAlg) new(alg int, key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if alg >= A128GCM && alg <= A256GCM {
		return cipher.NewGCM(b)
	}
	return ccm.NewCCM(b, a.tag, a.nonce)
}

// Params are the pre-established input parameters (RFC 8613 §3.2).
// MasterSecret, SenderID and RecipientID are mandatory; the rest default.
type Params struct {
	MasterSecret []byte
	MasterSalt   []byte // default empty
	SenderID     []byte
	RecipientID  []byte
	IDContext    []byte // nil: absent (info carries CBOR nil)
	AEAD         int    // 0: AES-CCM-16-64-128
	HKDF         int    // 0: HKDF SHA-256
	// SendKIDContext puts the ID Context in the 'kid context' of requests
	// (§5.1).
	SendKIDContext bool
}

// Reverse returns the peer's view: Sender and Recipient ID swapped
// (Figure 4).
func (p Params) Reverse() Params {
	p.SenderID, p.RecipientID = p.RecipientID, p.SenderID
	return p
}

// Context is a derived security context. It is safe for concurrent use:
// reading and increasing the Sender Sequence Number and checking and
// updating the Replay Window are atomic (§7.2, §7.4).
type Context struct {
	p                 Params
	alg               aeadAlg
	SenderKey         []byte
	RecipientKey      []byte
	CommonIV          []byte
	sender, recipient cipher.AEAD

	mu     sync.Mutex
	seq    uint64
	window replayWindow
}

// MaxSequence is the largest Sender Sequence Number (§7.2.1: < 2^40).
const MaxSequence = 1<<40 - 1

var (
	ErrDecode    = errors.New("oscore: failed to decode COSE")      // 4.02 (§8.2 step 2)
	ErrNoContext = errors.New("oscore: security context not found") // 4.01 (§8.2 step 2)
	ErrReplay    = errors.New("oscore: replay detected")            // 4.01 (§7.4)
	ErrDecrypt   = errors.New("oscore: decryption failed")          // 4.00 (§8.2 step 6)
	ErrSequence  = errors.New("oscore: sender sequence number exhausted")
	ErrNested    = errors.New("oscore: message already has an OSCORE option") // §4.1.3.7
	ErrNotOSCORE = errors.New("oscore: message has no OSCORE option")
)

// New derives a security context (§3.2.1).
func New(p Params) (*Context, error) {
	if p.AEAD == 0 {
		p.AEAD = AESCCM16_64_128
	}
	alg, ok := aeads[p.AEAD]
	if !ok {
		return nil, fmt.Errorf("oscore: unsupported AEAD algorithm %d", p.AEAD)
	}
	var h func() hash.Hash
	switch p.HKDF {
	case 0, HKDFSHA256, HMAC256:
		h = sha256.New
	case HMAC384:
		h = sha512.New384
	case HKDFSHA512, HMAC512:
		h = sha512.New
	default:
		return nil, fmt.Errorf("oscore: unsupported HKDF algorithm %d", p.HKDF)
	}
	if len(p.MasterSecret) == 0 {
		return nil, errors.New("oscore: master secret is mandatory")
	}
	maxID := alg.nonce - 6
	if len(p.SenderID) > maxID || len(p.RecipientID) > maxID {
		return nil, fmt.Errorf("oscore: Sender/Recipient ID longer than %d bytes (§3.3)", maxID)
	}
	c := &Context{p: p, alg: alg}
	derive := func(id []byte, typ string, l int) ([]byte, error) {
		var idctx any // CBOR nil when absent
		if p.IDContext != nil {
			idctx = p.IDContext
		}
		info, err := cbor.Marshal([]any{append([]byte{}, id...), idctx, p.AEAD, typ, l})
		if err != nil {
			return nil, err
		}
		return hkdf.Key(h, p.MasterSecret, p.MasterSalt, string(info), l)
	}
	var err error
	if c.SenderKey, err = derive(p.SenderID, "Key", alg.key); err != nil {
		return nil, err
	}
	if c.RecipientKey, err = derive(p.RecipientID, "Key", alg.key); err != nil {
		return nil, err
	}
	if c.CommonIV, err = derive(nil, "IV", alg.nonce); err != nil {
		return nil, err
	}
	if c.sender, err = alg.new(p.AEAD, c.SenderKey); err != nil {
		return nil, err
	}
	if c.recipient, err = alg.new(p.AEAD, c.RecipientKey); err != nil {
		return nil, err
	}
	return c, nil
}

// Params returns the input parameters.
func (c *Context) Params() Params { return c.p }

// SetSequence sets the Sender Sequence Number, e.g. restored from
// nonvolatile memory per Appendix B.1.1.
func (c *Context) SetSequence(n uint64) {
	c.mu.Lock()
	c.seq = n
	c.mu.Unlock()
}

// Sequence returns the next Sender Sequence Number.
func (c *Context) Sequence() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

// nextPIV reads and increments the Sender Sequence Number atomically.
func (c *Context) nextPIV() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seq > MaxSequence {
		return nil, ErrSequence
	}
	n := c.seq
	c.seq++
	return encodePIV(n), nil
}

// encodePIV drops leading zero bytes; 0 is 0x00 (§5).
func encodePIV(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	i := 0
	for i < 7 && b[i] == 0 {
		i++
	}
	return append([]byte(nil), b[i:]...)
}

func decodePIV(b []byte) uint64 {
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return n
}

// Nonce computes the AEAD nonce from the ID of the endpoint that generated
// the Partial IV and the Partial IV (§5.2).
func (c *Context) Nonce(idPIV, piv []byte) []byte {
	n := make([]byte, c.alg.nonce)
	n[0] = byte(len(idPIV))
	copy(n[c.alg.nonce-5-len(idPIV):], idPIV)
	copy(n[c.alg.nonce-len(piv):], piv)
	for i := range n {
		n[i] ^= c.CommonIV[i]
	}
	return n
}

// AAD builds the Enc_structure (§5.4) for a request kid and Partial IV.
// No Class I options are defined.
func (c *Context) AAD(requestKID, requestPIV []byte) []byte {
	arr, _ := cbor.Marshal([]any{1, []any{c.p.AEAD}, append([]byte{}, requestKID...), append([]byte{}, requestPIV...), []byte{}})
	aad, _ := cbor.Marshal([]any{"Encrypt0", []byte{}, arr})
	return aad
}

// replayWindow is the default anti-replay sliding window of 32 (§3.2.2,
// RFC 6347 §4.1.2.6) over received request Partial IVs.
type replayWindow struct {
	any  bool   // a Partial IV was accepted
	top  uint64 // highest accepted
	bits uint32 // bit i: top-i was accepted
}

func (w *replayWindow) seen(n uint64) bool {
	if !w.any || n > w.top {
		return false
	}
	d := w.top - n
	return d >= 32 || w.bits&(1<<d) != 0
}

func (w *replayWindow) accept(n uint64) {
	switch {
	case !w.any:
		w.any, w.top, w.bits = true, n, 1
	case n > w.top:
		d := n - w.top
		if d >= 32 {
			w.bits = 1
		} else {
			w.bits = w.bits<<d | 1
		}
		w.top = n
	default:
		w.bits |= 1 << (w.top - n)
	}
}

// ResetReplayWindow sets piv as the lower limit of the Replay Window:
// piv and every lower Partial IV count as received. A server that lost its
// window uses it once Echo proved a request fresh (Appendix B.1.2).
func (c *Context) ResetReplayWindow(piv uint64) {
	c.mu.Lock()
	c.window = replayWindow{any: true, top: piv, bits: ^uint32(0)}
	c.mu.Unlock()
}
