package mqtt

import (
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Operation codes (T Tbls 8.3.1-1 .. 8.3.4-1).
const (
	OpBootstrapRequest  = 0
	OpBootstrapWrite    = 1
	OpBootstrapRead     = 2
	OpBootstrapDelete   = 3
	OpBootstrapDiscover = 4
	OpBootstrapFinish   = 5
	OpBootstrapPack     = 6 // on the bs topic
	OpRegister          = 6 // on the rd topic
	OpUpdate            = 7
	OpDeregister        = 8
	OpRead              = 9
	OpReadComposite     = 10
	OpDiscover          = 11
	OpWriteReplace      = 12
	OpWritePartial      = 13
	OpWriteAttributes   = 14
	OpWriteComposite    = 15
	OpExecute           = 16
	OpCreate            = 17
	OpDelete            = 18
	OpObserve           = 20
	OpObserveComposite  = 21
	OpCancelObserve     = 22
	OpNotify            = 23
	OpSend              = 24
)

// Message is one LwM2M-over-MQTT message, a CBOR map keyed per T Tbl 8.7-1.
// A request carries Operation, a response Result (T §8.4). There are no
// keys for lt (less-than), pid or con (A-7); they are never invented here.
type Message struct {
	Operation *uint64  `cbor:"1,keyasint,omitempty"`
	Token     uint64   `cbor:"2,keyasint"`
	EP        *string  `cbor:"3,keyasint,omitempty"`
	PCT       *uint64  `cbor:"4,keyasint,omitempty"`
	URI       *string  `cbor:"5,keyasint,omitempty"`
	Paths     []byte   `cbor:"6,keyasint,omitempty"`
	Payload   Payload  `cbor:"7,keyasint,omitempty"`
	Lifetime  *uint64  `cbor:"8,keyasint,omitempty"`
	Version   *string  `cbor:"9,keyasint,omitempty"`
	B         *string  `cbor:"10,keyasint,omitempty"`
	SMS       *string  `cbor:"11,keyasint,omitempty"`
	Pmin      *uint64  `cbor:"12,keyasint,omitempty"`
	Pmax      *uint64  `cbor:"13,keyasint,omitempty"`
	Gt        *float64 `cbor:"14,keyasint,omitempty"`
	St        *float64 `cbor:"15,keyasint,omitempty"`
	Epmin     *uint64  `cbor:"16,keyasint,omitempty"`
	Epmax     *uint64  `cbor:"17,keyasint,omitempty"`
	Result    *uint64  `cbor:"18,keyasint,omitempty"`
	CT        *uint64  `cbor:"19,keyasint,omitempty"`
	Edge      *uint64  `cbor:"20,keyasint,omitempty"`
	Hqmax     *uint64  `cbor:"21,keyasint,omitempty"`
	Depth     *uint64  `cbor:"22,keyasint,omitempty"`
}

// Payload is key 7. The CDDL types it bstr, but the T §8.4 example puts a
// text string there (A-7), so decoding accepts both; encoding emits bstr.
type Payload []byte

func (p *Payload) UnmarshalCBOR(data []byte) error {
	var v any
	if err := cbor.Unmarshal(data, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case []byte:
		*p = x
	case string:
		*p = []byte(x)
	default:
		return fmt.Errorf("mqtt: payload is %T, want bstr or tstr", v)
	}
	return nil
}

var encMode, _ = cbor.CoreDetEncOptions().EncMode() // deterministic: sorted keys

// Marshal encodes m as deterministic CBOR.
func Marshal(m *Message) ([]byte, error) { return encMode.Marshal(m) }

// Unmarshal decodes a message. An Outer_Wrapper (T §8.6) whose msg-wrapper
// is nil is unwrapped; a COSE-protected one needs the endpoint's /23 key
// and is refused here (see unwrap).
func Unmarshal(b []byte) (*Message, error) { return unwrap(b, nil) }

func decode(b []byte) (*Message, error) {
	var m Message
	if err := cbor.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if (m.Operation == nil) == (m.Result == nil) {
		return nil, errors.New("mqtt: message needs exactly one of operation (1) or result (18)")
	}
	return &m, nil
}

func u64(v uint64) *uint64 { return &v }
func str(v string) *string { return &v }
