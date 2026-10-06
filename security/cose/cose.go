// Package cose implements the single-recipient, direct-key COSE_Encrypt0
// structure of RFC 8152 §5.2-§5.3, the form LwM2M uses to protect MQTT
// messages end to end (T §8.6, Core E.10).
package cose

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"

	"github.com/fiumaralabs/dtls/v3/pkg/crypto/ccm"
	"github.com/fxamacker/cbor/v2"
)

// AEAD algorithms (RFC 8152 Tbls 9, 10), the values of /23/x/1.
const (
	A128GCM          = 1
	A192GCM          = 2
	A256GCM          = 3
	AESCCM16_64_128  = 10
	AESCCM16_64_256  = 11
	AESCCM64_64_128  = 12
	AESCCM64_64_256  = 13
	AESCCM16_128_128 = 30
	AESCCM16_128_256 = 31
	AESCCM64_128_128 = 32
	AESCCM64_128_256 = 33
)

// Header labels (RFC 8152 Tbl 2).
const (
	HdrAlg = 1
	HdrKID = 4
	HdrIV  = 5
)

type algInfo struct{ key, nonce, tag int }

// ponytail: same table as oscore's (unexported there); merge if a third user appears.
var algs = map[int64]algInfo{
	A128GCM: {16, 12, 16}, A192GCM: {24, 12, 16}, A256GCM: {32, 12, 16},
	AESCCM16_64_128: {16, 13, 8}, AESCCM16_64_256: {32, 13, 8},
	AESCCM64_64_128: {16, 7, 8}, AESCCM64_64_256: {32, 7, 8},
	AESCCM16_128_128: {16, 13, 16}, AESCCM16_128_256: {32, 13, 16},
	AESCCM64_128_128: {16, 7, 16}, AESCCM64_128_256: {32, 7, 16},
}

// ErrDecrypt is returned when authentication of a message fails.
var ErrDecrypt = errors.New("cose: decryption failed")

// NonceSize returns the IV length alg needs (RFC 8152 §10.1, §10.2).
func NonceSize(alg int64) (int, error) {
	a, ok := algs[alg]
	if !ok {
		return 0, fmt.Errorf("cose: unsupported AEAD algorithm %d", alg)
	}
	return a.nonce, nil
}

// AEAD returns the cipher for alg with key, checking the key length.
func AEAD(alg int64, key []byte) (cipher.AEAD, error) {
	a, ok := algs[alg]
	if !ok {
		return nil, fmt.Errorf("cose: unsupported AEAD algorithm %d", alg)
	}
	if len(key) != a.key {
		return nil, fmt.Errorf("cose: algorithm %d needs a %d-byte key, have %d", alg, a.key, len(key))
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if alg <= A256GCM {
		return cipher.NewGCM(b)
	}
	return ccm.NewCCM(b, a.tag, a.nonce)
}

var enc, _ = cbor.CoreDetEncOptions().EncMode()

// encrypt0 is COSE_Encrypt0 = [protected: bstr, unprotected: map, ciphertext: bstr / nil].
type encrypt0 struct {
	_           struct{} `cbor:",toarray"`
	Protected   []byte
	Unprotected map[int64]cbor.RawMessage
	Ciphertext  []byte
}

// aad is the Enc_structure (RFC 8152 §5.3) for COSE_Encrypt0.
func aad(protected, external []byte) []byte {
	if external == nil {
		external = []byte{}
	}
	b, _ := enc.Marshal([]any{"Encrypt0", protected, external})
	return b
}

// Seal encrypts plaintext as an untagged COSE_Encrypt0 with alg in the
// protected header and kid (omitted when empty) and iv in the unprotected
// one. iv must be NonceSize(alg) bytes and never repeat under one key.
func Seal(alg int64, key, kid, iv, plaintext, external []byte) ([]byte, error) {
	c, err := AEAD(alg, key)
	if err != nil {
		return nil, err
	}
	if len(iv) != c.NonceSize() {
		return nil, fmt.Errorf("cose: IV is %d bytes, algorithm %d needs %d", len(iv), alg, c.NonceSize())
	}
	prot, _ := enc.Marshal(map[int64]int64{HdrAlg: alg})
	un := map[int64]cbor.RawMessage{HdrIV: must(enc.Marshal(iv))}
	if len(kid) > 0 {
		un[HdrKID] = must(enc.Marshal(kid))
	}
	return enc.Marshal(encrypt0{Protected: prot, Unprotected: un, Ciphertext: c.Seal(nil, iv, plaintext, aad(prot, external))})
}

func must(b []byte, _ error) []byte { return b }

// Open decrypts a COSE_Encrypt0, tagged (16) or not. The algorithm in the
// message (protected or unprotected header, RFC 8152 §3.1) must be alg,
// so a peer cannot pick a weaker one. It returns the plaintext and the kid
// header (nil when absent).
func Open(alg int64, key, msg, external []byte) (plaintext, kid []byte, err error) {
	msg = bytes.TrimPrefix(msg, []byte{0xd0}) // tag 16, COSE_Encrypt0_Tagged
	var m encrypt0
	if err := cbor.Unmarshal(msg, &m); err != nil {
		return nil, nil, fmt.Errorf("cose: %w", err)
	}
	var prot map[int64]cbor.RawMessage
	if len(m.Protected) > 0 {
		if err := cbor.Unmarshal(m.Protected, &prot); err != nil {
			return nil, nil, fmt.Errorf("cose: protected header: %w", err)
		}
	}
	for l := range prot {
		if _, dup := m.Unprotected[l]; dup {
			return nil, nil, fmt.Errorf("cose: label %d in both headers (RFC 8152 §3)", l)
		}
	}
	hdr := func(l int64) cbor.RawMessage {
		if v, ok := prot[l]; ok {
			return v
		}
		return m.Unprotected[l]
	}
	var got int64
	if err := cbor.Unmarshal(hdr(HdrAlg), &got); err != nil || got != alg {
		return nil, nil, fmt.Errorf("cose: algorithm %d, want %d", got, alg)
	}
	var iv []byte
	if err := cbor.Unmarshal(hdr(HdrIV), &iv); err != nil {
		return nil, nil, errors.New("cose: no IV")
	}
	if k := hdr(HdrKID); k != nil {
		if err := cbor.Unmarshal(k, &kid); err != nil {
			return nil, nil, fmt.Errorf("cose: kid: %w", err)
		}
	}
	c, err := AEAD(alg, key)
	if err != nil {
		return nil, nil, err
	}
	if len(iv) != c.NonceSize() || m.Ciphertext == nil {
		return nil, nil, ErrDecrypt
	}
	pt, err := c.Open(nil, iv, m.Ciphertext, aad(m.Protected, external))
	if err != nil {
		return nil, nil, ErrDecrypt
	}
	return pt, kid, nil
}
