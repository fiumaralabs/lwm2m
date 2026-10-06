package mqttbinding

import (
	"crypto/rand"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/cose"
	"github.com/fxamacker/cbor/v2"
)

// COSEKey is the keying material of one /23 LwM2M COSE instance (Core
// E.10). When the client's /0 instance for this Server links it
// (/0/x/27), every message on the endpoint's topic is a COSE_Encrypt0
// (T §8.8) under this one symmetric key, in both directions.
type COSEKey struct {
	KID string // res 0; sent as the kid header, checked when a peer sends one
	Alg int64  // res 1, RFC 8152 Tbl 9/10 (e.g. cose.AESCCM16_64_128)
	// Key is res 2. E.10 types it String, so the key octets are the
	// string's UTF-8 octets and must be valid UTF-8.
	// ponytail: no encoding (hex, base64) is defined for /23/x/2; raw octets until OMA says otherwise.
	Key []byte
}

// Validate checks the algorithm, the key length and the String typing.
func (k COSEKey) Validate() error {
	if _, err := cose.AEAD(k.Alg, k.Key); err != nil {
		return err
	}
	if !utf8.Valid(k.Key) || !utf8.ValidString(k.KID) {
		return errors.New("mqttbinding: /23 KID and Key are String resources and must be UTF-8 (Core E.10)")
	}
	return nil
}

// Nodes returns the /23 instance at inst for a Bootstrap-Write.
func (k COSEKey) Nodes(inst uint16) ([]lwm2m.Node, error) {
	if err := k.Validate(); err != nil {
		return nil, err
	}
	p := lwm2m.NewPath(23, inst)
	return []lwm2m.Node{
		lwm2m.ValueNode(p.Append(0), lwm2m.String(k.KID)),
		lwm2m.ValueNode(p.Append(1), lwm2m.Integer(k.Alg)),
		lwm2m.ValueNode(p.Append(2), lwm2m.String(string(k.Key))),
	}, nil
}

// outer is the Outer_Wrapper of T §8.6.
type outer struct {
	Wrapper cbor.RawMessage `cbor:"1,keyasint"`
	Inner   cbor.RawMessage `cbor:"2,keyasint"`
}

// seal protects m as Outer_Wrapper{1: bstr .cbor [COSE_Encrypt0], 2: {}}.
// The plaintext is the whole LwM2M message map. T §8.6 does not say what
// key 2 holds once the message is encrypted; repeating it in clear would
// defeat the confidentiality §8.6 asks for, so it is an empty map.
// The external AAD is empty: §8.6 defines none.
// ponytail: random IVs and no replay window; T §8 defines neither (no
// sequence number or replay rule for COSE). Add a counter if OMA does.
func seal(k *COSEKey, m *Message) ([]byte, error) {
	pt, err := Marshal(m)
	if err != nil {
		return nil, err
	}
	n, err := cose.NonceSize(k.Alg)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, n)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	c0, err := cose.Seal(k.Alg, k.Key, []byte(k.KID), iv, pt, nil)
	if err != nil {
		return nil, err
	}
	arr, err := encMode.Marshal([]cbor.RawMessage{c0})
	if err != nil {
		return nil, err
	}
	w, _ := encMode.Marshal(arr) // bstr .cbor Msg_Wrapper
	return encMode.Marshal(outer{Wrapper: w, Inner: []byte{0xa0}})
}

var errUnprotected = errors.New("mqttbinding: message is not COSE-protected")

// unwrap decodes b. With k nil a COSE-protected message is refused;
// with k set an unprotected one is decoded but reported with
// errUnprotected so the caller can answer it (T §8.8: COSE is a MUST).
func unwrap(b []byte, k *COSEKey) (*Message, error) {
	var o outer
	if err := cbor.Unmarshal(b, &o); err == nil && len(o.Inner) > 0 && o.Inner[0]>>5 == 5 { // key 2 is a map
		if len(o.Wrapper) > 0 && o.Wrapper[0] != 0xf6 { // msg-wrapper is not nil
			if k == nil {
				return nil, errors.New("mqttbinding: COSE-protected message, no /23 key for this endpoint")
			}
			pt, err := openWrapper(k, o.Wrapper)
			if err != nil {
				return nil, err
			}
			return decode(pt)
		}
		b = o.Inner
	}
	m, err := decode(b)
	if err == nil && k != nil {
		return m, errUnprotected
	}
	return m, err
}

// openWrapper decrypts the first COSE_Encrypt0 of a Msg_Wrapper that
// opens under k. COSE_Encrypt (with recipients) needs key distribution
// /23 cannot express, so it is not tried.
func openWrapper(k *COSEKey, w cbor.RawMessage) ([]byte, error) {
	var inner []byte
	if err := cbor.Unmarshal(w, &inner); err != nil {
		return nil, fmt.Errorf("mqttbinding: msg-wrapper is not a bstr: %w", err)
	}
	var list []cbor.RawMessage
	if err := cbor.Unmarshal(inner, &list); err != nil {
		return nil, fmt.Errorf("mqttbinding: msg-wrapper is not a COSE array: %w", err)
	}
	err := errors.New("mqttbinding: empty msg-wrapper")
	for _, c := range list {
		var pt, kid []byte
		if pt, kid, err = cose.Open(k.Alg, k.Key, c, nil); err != nil {
			continue
		}
		if kid != nil && string(kid) != k.KID {
			err = fmt.Errorf("mqttbinding: kid %q, want %q", kid, k.KID)
			continue
		}
		return pt, nil
	}
	return nil, err
}
