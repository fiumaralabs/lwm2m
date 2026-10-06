// Package cbor implements the plain CBOR content format (60,
// application/cbor), Core 1.2.2 §7.5.3: one resource or resource-instance
// value, typed per Table C-2 ("CBOR, LwM2M CBOR and SenML CBOR" column).
//
// It also exports Raw and Coerce, the CBOR-item-to-value and
// value-to-resource-type rules that the SenML codecs share.
package cbor

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	fx "github.com/fxamacker/cbor/v2"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

var errCBOR = errors.New("cbor")

// em encodes floats at their Go width (float64 -> 0xfb, never shortened, as
// Zephyr and Anjay do; Table C-2 allows any width) and keeps NaN/Inf 64-bit.
var em, _ = fx.EncOptions{
	ShortestFloat: fx.ShortestFloatNone,
	NaNConvert:    fx.NaNConvertNone,
	InfConvert:    fx.InfConvertNone,
}.EncMode()

// Marshal encodes v with the package's encoding options.
func Marshal(v any) ([]byte, error) { return em.Marshal(v) }

// Codec is the CBOR (60) codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatCBOR }

func checkBase(base lwm2m.Path) error {
	if !base.IsResource() && !base.IsResourceInstance() {
		return fmt.Errorf("%w: needs a resource or resource-instance path, got %v", errCBOR, base)
	}
	return nil
}

// Encode writes the single value at base. Integers use the shortest form
// (RFC 8949 §3.1), floats are 64-bit, Time is tag 1 + epoch integer
// (RFC 8949 §3.4.2), Objlnk is the text "oid:iid", Opaque a byte string.
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	if err := codec.RejectPrefix(nodes); err != nil {
		return nil, err
	}
	if err := checkBase(base); err != nil {
		return nil, err
	}
	if len(nodes) != 1 || nodes[0].Kind != lwm2m.KindValue || nodes[0].Path != base {
		return nil, fmt.Errorf("%w: needs exactly one value at %v", errCBOR, base)
	}
	return EncodeValue(nodes[0].Value)
}

// EncodeValue encodes one value per Table C-2.
func EncodeValue(v lwm2m.Value) ([]byte, error) {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return em.Marshal(v.Str)
	case lwm2m.TypeInteger:
		return em.Marshal(v.Int)
	case lwm2m.TypeUnsigned:
		return em.Marshal(v.Uint)
	case lwm2m.TypeFloat:
		return em.Marshal(v.Float)
	case lwm2m.TypeBoolean:
		return em.Marshal(v.Bool)
	case lwm2m.TypeOpaque:
		return em.Marshal(append([]byte{}, v.Bytes...)) // non-nil: 0x40, never null
	case lwm2m.TypeTime:
		return em.Marshal(fx.Tag{Number: 1, Content: v.Int})
	case lwm2m.TypeObjlnk:
		return em.Marshal(v.Link.String())
	case lwm2m.TypeNone:
		return []byte{0xf6}, nil // Table C-2: none = null
	}
	return nil, fmt.Errorf("%w: cannot encode %v", errCBOR, v.Type)
}

// Decode reads exactly one CBOR item (trailing bytes are an error) and types
// it from the schema of base, or infers the type without one.
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	if err := checkBase(base); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty payload", errCBOR)
	}
	var x any
	if err := fx.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("%w: %v", errCBOR, err)
	}
	v, err := Raw(x)
	if err != nil {
		return nil, err
	}
	t := lwm2m.TypeNone
	if s != nil {
		if def, ok := s.Resource(base.Truncate(3)); ok {
			if def.Multiple && base.IsResource() {
				return nil, fmt.Errorf("%w: %v is multi-instance", errCBOR, base)
			}
			t = def.Type
		}
	}
	if v, err = Coerce(v, t); err != nil {
		return nil, err
	}
	return []lwm2m.Node{lwm2m.ValueNode(base, v)}, nil
}

// Raw converts an item decoded by fxamacker/cbor into `any` to an untyped
// value: unsigned -> Integer (Unsigned above MaxInt64), negative -> Integer,
// float (any width) -> Float, text -> String, bytes -> Opaque, bool ->
// Boolean, null -> TypeNone, tag 0/1 -> Time, tag 4 decimal fraction -> Float.
func Raw(x any) (lwm2m.Value, error) {
	switch x := x.(type) {
	case uint64:
		if x <= math.MaxInt64 {
			return lwm2m.Integer(int64(x)), nil
		}
		return lwm2m.Unsigned(x), nil
	case int64:
		return lwm2m.Integer(x), nil
	case float64:
		return lwm2m.Float(x), nil
	case string:
		return lwm2m.String(x), nil
	case []byte:
		return lwm2m.Opaque(x), nil
	case bool:
		return lwm2m.Boolean(x), nil
	case nil:
		return lwm2m.Value{}, nil
	case time.Time: // tag 0 (RFC 3339 text, Zephyr) or tag 1 (epoch)
		return lwm2m.TimeOf(x), nil
	case fx.Tag:
		if x.Number == 4 { // RFC 8949 §3.4.4 decimal fraction [exp, mantissa] (Leshan bt)
			if a, ok := x.Content.([]any); ok && len(a) == 2 {
				e, err1 := Raw(a[0])
				m, err2 := Raw(a[1])
				if err1 == nil && err2 == nil && e.Type == lwm2m.TypeInteger {
					if f, ok := asFloat(m); ok {
						return lwm2m.Float(f * math.Pow10(int(e.Int))), nil
					}
				}
			}
		}
		return lwm2m.Value{}, fmt.Errorf("%w: unsupported tag %d", errCBOR, x.Number)
	}
	return lwm2m.Value{}, fmt.Errorf("%w: unsupported item %T", errCBOR, x)
}

// Coerce converts an untyped value to resource type t (TypeNone keeps it as
// is). Numbers convert when exact (an integral float is accepted for an
// integer resource); a text Objlnk drops one trailing NUL (Zephyr, C7) and
// accepts the "FFFF:FFFF" spelling of the null link (Core §7.5.6-4, A-24).
// Anything else that does not match t is an error (DT-02 type check).
func Coerce(v lwm2m.Value, t lwm2m.Type) (lwm2m.Value, error) {
	if t == lwm2m.TypeNone || t == v.Type {
		return v, nil
	}
	ok := false
	switch t {
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		var i int64
		if i, ok = asInt(v); ok {
			return lwm2m.Value{Type: t, Int: i}, nil
		}
	case lwm2m.TypeUnsigned:
		var u uint64
		if u, ok = asUint(v); ok {
			return lwm2m.Unsigned(u), nil
		}
	case lwm2m.TypeFloat:
		var f float64
		if f, ok = asFloat(v); ok {
			return lwm2m.Float(f), nil
		}
	case lwm2m.TypeCorelnk:
		if v.Type == lwm2m.TypeString {
			return lwm2m.Corelnk(v.Str), nil
		}
	case lwm2m.TypeObjlnk:
		if v.Type == lwm2m.TypeString {
			s := strings.TrimSuffix(v.Str, "\x00")
			if strings.EqualFold(s, "FFFF:FFFF") {
				return lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: lwm2m.NullObjLink}, nil
			}
			l, err := lwm2m.ParseObjLink(s)
			if err != nil {
				return lwm2m.Value{}, err
			}
			return lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: l}, nil
		}
	}
	return lwm2m.Value{}, fmt.Errorf("%w: %v value %v for a %v resource", errCBOR, v.Type, v, t)
}

func asInt(v lwm2m.Value) (int64, bool) {
	switch v.Type {
	case lwm2m.TypeInteger:
		return v.Int, true
	case lwm2m.TypeUnsigned:
		return int64(v.Uint), v.Uint <= math.MaxInt64
	case lwm2m.TypeFloat:
		f := v.Float
		return int64(f), f == math.Trunc(f) && f >= -(1<<63) && f < 1<<63
	}
	return 0, false
}

func asUint(v lwm2m.Value) (uint64, bool) {
	switch v.Type {
	case lwm2m.TypeInteger:
		return uint64(v.Int), v.Int >= 0
	case lwm2m.TypeUnsigned:
		return v.Uint, true
	case lwm2m.TypeFloat:
		f := v.Float
		return uint64(f), f == math.Trunc(f) && f >= 0 && f < 1<<64
	}
	return 0, false
}

func asFloat(v lwm2m.Value) (float64, bool) {
	switch v.Type {
	case lwm2m.TypeInteger:
		return float64(v.Int), true
	case lwm2m.TypeUnsigned:
		return float64(v.Uint), true
	case lwm2m.TypeFloat:
		return v.Float, true
	}
	return 0, false
}

// AsFloat returns a numeric value as float64.
func AsFloat(v lwm2m.Value) (float64, bool) { return asFloat(v) }
