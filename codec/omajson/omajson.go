// Package omajson implements the legacy OMA LwM2M JSON format,
// application/vnd.oma.lwm2m+json, Content-Format 11543 (TS 1.0.2 §6.4.4,
// Core 1.1.1 §7.4.5.1, Core 1.2.2 §7.5.6.1). Servers accept it from 1.0
// clients (FMT-02, FMT-10); the registry also routes 50 and 1543 here.
package omajson

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

// Codec is the OMA JSON codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatOMAJSON }

// Document is a raw OMA JSON document. Field order is serialization order.
type Document struct {
	BN *string      `json:"bn,omitempty"`
	E  []Entry      `json:"e"`
	BT *json.Number `json:"bt,omitempty"`
}

// Entry is one element of "e". Exactly one of V, SV, BV, OV carries the value.
type Entry struct {
	N  *string      `json:"n,omitempty"`
	V  *json.Number `json:"v,omitempty"`
	SV *string      `json:"sv,omitempty"`
	BV *bool        `json:"bv,omitempty"`
	OV *string      `json:"ov,omitempty"`
	T  *json.Number `json:"t,omitempty"`
}

var errFormat = errors.New("omajson: invalid payload")

func fail(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errFormat, fmt.Sprintf(format, a...))
}

// DecodeDocument parses a raw document without interpreting it.
func DecodeDocument(data []byte) (*Document, error) {
	var d Document
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%w: %v", errFormat, err)
	}
	return &d, nil
}

// EncodeDocument serializes d compactly, without HTML escaping.
func EncodeDocument(d *Document) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Decode resolves names as plain bn+n concatenation, absolute times as bt+t,
// and types values from s (inferring them for unknown resources).
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	d, err := DecodeDocument(data)
	if err != nil {
		return nil, err
	}
	if d.E == nil {
		return nil, fail(`missing "e"`)
	}
	if len(d.E) == 0 {
		return empty(base, s)
	}
	bn := ""
	if d.BN != nil {
		bn = *d.BN
	}
	var bt float64
	if d.BT != nil {
		if bt, err = d.BT.Float64(); err != nil {
			return nil, fail("bt: %v", err)
		}
	}
	type key struct {
		p       lwm2m.Path
		hasTime bool
		t       float64
	}
	seen := map[key]bool{}
	out := make([]lwm2m.Node, 0, len(d.E))
	for _, e := range d.E {
		name := bn
		if e.N != nil {
			name += *e.N
		}
		p, err := parsePath(name)
		if err != nil {
			return nil, err
		}
		if p.Len() < 3 || !p.HasPrefix(base) {
			return nil, fail("%s is not a resource under %s", p, base)
		}
		var def lwm2m.ResourceDef
		known := false
		if s != nil {
			def, known = s.Resource(p.Truncate(3))
		}
		v, err := value(e, def.Type, known && def.Type != lwm2m.TypeNone)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		n := lwm2m.ValueNode(p, v)
		if e.T != nil || d.BT != nil {
			t := 0.0
			if e.T != nil {
				if t, err = e.T.Float64(); err != nil {
					return nil, fail("t: %v", err)
				}
			}
			n.Time, n.HasTime = bt+t, true
		}
		k := key{p, n.HasTime, n.Time}
		if seen[k] {
			return nil, fail("duplicate entry for %s", p)
		}
		seen[k] = true
		out = append(out, n)
	}
	return out, nil
}

// empty interprets {"e":[]}: no instances, an empty instance or an empty
// multi-instance resource, depending on the request path.
func empty(base lwm2m.Path, s lwm2m.Schema) ([]lwm2m.Node, error) {
	switch base.Len() {
	case 0, 1:
		return []lwm2m.Node{}, nil
	case 2:
		return []lwm2m.Node{{Path: base, Kind: lwm2m.KindEmptyInstance}}, nil
	case 3:
		if s != nil {
			if def, ok := s.Resource(base); ok && def.Multiple {
				return []lwm2m.Node{{Path: base, Kind: lwm2m.KindEmptyMultiple}}, nil
			}
		}
	}
	return nil, fail("no value for single resource %s", base)
}

func value(e Entry, t lwm2m.Type, known bool) (lwm2m.Value, error) {
	n := 0
	for _, set := range []bool{e.V != nil, e.SV != nil, e.BV != nil, e.OV != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return lwm2m.Value{}, fail("entry needs exactly one of v, sv, bv, ov (has %d)", n)
	}
	mismatch := func(key string) (lwm2m.Value, error) {
		return lwm2m.Value{}, fail("%q cannot carry a %v", key, t)
	}
	switch {
	case e.V != nil:
		lit := e.V.String()
		if !known {
			t = lwm2m.TypeInteger
			if strings.ContainsAny(lit, ".eE") {
				t = lwm2m.TypeFloat
			}
		}
		switch t {
		case lwm2m.TypeInteger, lwm2m.TypeTime:
			i, err := strconv.ParseInt(lit, 10, 64)
			if err != nil {
				return lwm2m.Value{}, fail("integer %s: %v", lit, err)
			}
			return lwm2m.Value{Type: t, Int: i}, nil
		case lwm2m.TypeUnsigned:
			u, err := strconv.ParseUint(lit, 10, 64)
			if err != nil {
				return lwm2m.Value{}, fail("unsigned %s: %v", lit, err)
			}
			return lwm2m.Unsigned(u), nil
		case lwm2m.TypeFloat:
			f, err := strconv.ParseFloat(lit, 64)
			if err != nil {
				return lwm2m.Value{}, fail("float %s: %v", lit, err)
			}
			return lwm2m.Float(f), nil
		}
		return mismatch("v")
	case e.SV != nil:
		switch {
		case !known || t == lwm2m.TypeString:
			return lwm2m.String(*e.SV), nil
		case t == lwm2m.TypeCorelnk:
			return lwm2m.Corelnk(*e.SV), nil
		case t == lwm2m.TypeOpaque:
			b, err := base64.StdEncoding.DecodeString(*e.SV)
			if err != nil {
				return lwm2m.Value{}, fail("opaque: %v", err)
			}
			return lwm2m.Opaque(b), nil
		}
		return mismatch("sv")
	case e.BV != nil:
		if known && t != lwm2m.TypeBoolean {
			return mismatch("bv")
		}
		return lwm2m.Boolean(*e.BV), nil
	default:
		if known && t != lwm2m.TypeObjlnk {
			return mismatch("ov")
		}
		l, err := lwm2m.ParseObjLink(*e.OV)
		if err != nil {
			return lwm2m.Value{}, fail("%v", err)
		}
		return lwm2m.Value{Type: lwm2m.TypeObjlnk, Link: l}, nil
	}
}

// parsePath is lwm2m.ParsePath that also accepts ID 65535 (Zephyr's test
// object): MAX_ID is reserved in meaning, not unrepresentable.
// ponytail: drop once lwm2m.ParsePath accepts 65535.
func parsePath(s string) (lwm2m.Path, error) {
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return lwm2m.Root, nil
	}
	segs := strings.Split(s, "/")
	if len(segs) > 4 {
		return lwm2m.Path{}, fail("name %q: more than 4 levels", s)
	}
	ids := make([]uint16, len(segs))
	for i, seg := range segs {
		if seg == "" || strings.TrimLeft(seg, "0123456789") != "" {
			return lwm2m.Path{}, fail("name %q: bad segment %q", s, seg)
		}
		v, err := strconv.ParseUint(seg, 10, 16)
		if err != nil {
			return lwm2m.Path{}, fail("name %q: %v", s, err)
		}
		ids[i] = uint16(v)
	}
	return lwm2m.NewPath(ids...), nil
}

// Encode writes bn as the request path cut to the instance level with a
// trailing slash ("/" for the root), n relative to it, and absolute "t" per
// entry (no bt). Entry order follows nodes.
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	bn := base.Truncate(2).String()
	if !strings.HasSuffix(bn, "/") {
		bn += "/"
	}
	d := &Document{BN: &bn, E: []Entry{}}
	for _, n := range nodes {
		if !n.Path.HasPrefix(base) {
			return nil, fail("%s is outside %s", n.Path, base)
		}
		if n.Kind != lwm2m.KindValue {
			// An empty container is only expressible as the whole payload.
			if len(nodes) != 1 || n.Path != base {
				return nil, fail("empty container %s must be the only node at the request path", n.Path)
			}
			return EncodeDocument(d)
		}
		if n.Path.Len() < 3 {
			return nil, fail("value at non-resource path %s", n.Path)
		}
		name := strings.TrimPrefix(n.Path.String(), bn)
		e := Entry{N: &name}
		if err := setValue(&e, n.Value); err != nil {
			return nil, fmt.Errorf("%s: %w", n.Path, err)
		}
		if n.HasTime {
			t, err := number(n.Time)
			if err != nil {
				return nil, err
			}
			e.T = &t
		}
		d.E = append(d.E, e)
	}
	return EncodeDocument(d)
}

func setValue(e *Entry, v lwm2m.Value) error {
	var num json.Number
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		e.SV = &v.Str
	case lwm2m.TypeOpaque:
		s := base64.StdEncoding.EncodeToString(v.Bytes)
		e.SV = &s
	case lwm2m.TypeBoolean:
		e.BV = &v.Bool
	case lwm2m.TypeObjlnk:
		s := v.Link.String()
		e.OV = &s
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		num = json.Number(strconv.FormatInt(v.Int, 10))
		e.V = &num
	case lwm2m.TypeUnsigned:
		num = json.Number(strconv.FormatUint(v.Uint, 10))
		e.V = &num
	case lwm2m.TypeFloat:
		s, err := formatFloat(v.Float)
		if err != nil {
			return err
		}
		num = json.Number(s)
		e.V = &num
	default:
		return fail("type %v has no OMA JSON form", v.Type)
	}
	return nil
}

// formatFloat writes plain decimals with at least one fraction digit ("3.0",
// as Zephyr and Leshan do) and exponent form outside [1e-6, 1e21).
func formatFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fail("float %v has no JSON form", f)
	}
	if a := math.Abs(f); a != 0 && (a < 1e-6 || a >= 1e21) {
		return strconv.FormatFloat(f, 'e', -1, 64), nil
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s, nil
}

// number writes a timestamp: integral seconds as an integer.
func number(f float64) (json.Number, error) {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return json.Number(strconv.FormatInt(int64(f), 10)), nil
	}
	s, err := formatFloat(f)
	return json.Number(s), err
}
