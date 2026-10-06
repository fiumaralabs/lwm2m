// Package senml implements the SenML content formats of LwM2M 1.2.2:
// SenML JSON (110) and SenML CBOR (112) per RFC 8428 and Core §7.5.6/§7.5.7,
// and SenML-ETCH JSON (320) and CBOR (322) per RFC 8790. All four share one
// record model (Record) and one name/time resolution.
//
// Emitted form (always spec form; decoders also take the client variants):
//   - Request path is the root (Send, composite): every record carries its
//     full path in "n", no "bn" (Core Tables 7.5.6-7, -9; Send §6.4.6).
//   - Request path /o/i/r is the only node path (single resource): "bn" is
//     the full path, no "n" (Table 7.5.6-5).
//   - Otherwise: "bn" = request path + "/" on the first record, "n" relative
//     (Table 7.5.6-2, §7.5.7 example).
//   - Records of one object instance are contiguous, in order of the
//     instance's first appearance, resources sorted by ID (FMT-05).
//   - Times: "bt" on the first timestamped record, "t" relative to it.
//   - Keys in the order bn, bt, n, <value>, t. Objlnk under "vlo" (also a
//     text key in CBOR, §7.5.7). Opaque "vd" is base64url without padding in
//     JSON (Table C-2), a byte string in CBOR. Floats are 64-bit in CBOR.
package senml

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	cborc "github.com/fiumaralabs/lwm2m/codec/cbor"
)

var errSenML = errors.New("senml")

// Record is one SenML record before name and time resolution.
//
// Field names the value field: "" (none), "v", "vs", "vb", "vd" or "vlo".
// Value holds it untyped: v is Integer, Unsigned or Float (String for a
// text "v", a client quirk; TypeNone for ETCH "v": null); vs and vlo are
// String; vb Boolean; vd Opaque.
//
// Extra lists the other fields seen (u and bu excluded); they are ignored,
// except in a SenML-ETCH Fetch Pack, which must reject them.
type Record struct {
	BaseName string
	BaseTime float64
	Name     string
	Time     float64
	Field    string
	Value    lwm2m.Value
	Extra    []string
}

// Codec is one of the four SenML codecs. RootPath is the client's
// alternate path (T §6.4.1, e.g. "/lwm2m") or a gateway end-device prefix
// (GW §9, e.g. "/d01"): it is prepended to every emitted name and stripped
// from decoded names that carry it.
type Codec struct {
	format   lwm2m.ContentFormat
	RootPath string
}

var (
	JSON     = Codec{format: lwm2m.FormatSenMLJSON}
	CBOR     = Codec{format: lwm2m.FormatSenMLCBOR}
	ETCHJSON = Codec{format: lwm2m.FormatSenMLETCHJSON}
	ETCHCBOR = Codec{format: lwm2m.FormatSenMLETCHCBOR}
)

func init() {
	for _, c := range []Codec{JSON, CBOR, ETCHJSON, ETCHCBOR} {
		codec.Register(c)
	}
}

func (c Codec) Format() lwm2m.ContentFormat { return c.format }

// WithRootPath returns c using root as alternate path / gateway prefix.
func (c Codec) WithRootPath(root string) Codec {
	c.RootPath = strings.TrimSuffix(root, "/")
	return c
}

func (c Codec) isCBOR() bool {
	return c.format == lwm2m.FormatSenMLCBOR || c.format == lwm2m.FormatSenMLETCHCBOR
}

func (c Codec) isETCH() bool {
	return c.format == lwm2m.FormatSenMLETCHJSON || c.format == lwm2m.FormatSenMLETCHCBOR
}

// EncodeRecords serializes records as they are.
func (c Codec) EncodeRecords(rs []Record) ([]byte, error) {
	if c.isCBOR() {
		return encodeCBOR(rs)
	}
	return encodeJSON(rs)
}

// DecodeRecords parses records without resolving them. An empty payload is
// an empty pack.
func (c Codec) DecodeRecords(data []byte) ([]Record, error) {
	if c.isCBOR() {
		return decodeCBOR(data)
	}
	return decodeJSON(data)
}

// resolved is a record with its name and time resolved (RFC 8428 §4.6).
type resolved struct {
	Record
	name string
	time float64
}

func resolve(rs []Record) []resolved {
	out := make([]resolved, len(rs))
	bn, bt := "", 0.0
	for i, r := range rs {
		if r.BaseName != "" {
			bn = r.BaseName
		}
		if r.BaseTime != 0 { // ponytail: an explicit "bt":0 cannot reset the base time
			bt = r.BaseTime
		}
		t := bt + r.Time
		if r.Time != 0 && bt != 0 {
			t = math.Round(t*1e6) / 1e6 // microsecond resolution: undo float noise of bt+t
		}
		out[i] = resolved{r, bn + r.Name, t}
	}
	return out
}

// path turns a resolved name into a path, stripping RootPath when present.
func (c Codec) path(name string) (lwm2m.Path, error) {
	if c.RootPath != "" {
		if rest, ok := strings.CutPrefix(name, c.RootPath); ok && (rest == "" || rest[0] == '/') {
			name = rest
			if name == "" {
				name = "/"
			}
		}
	}
	p, err := lwm2m.ParsePath(name)
	if err != nil {
		return p, fmt.Errorf("%w: name %q: %v", errSenML, name, err)
	}
	return p, nil
}

// Decode resolves the pack into value nodes. Numeric "v" values are typed by
// the schema (Integer, Unsigned, Float, Time), else inferred (integer
// literal -> Integer, or Unsigned above MaxInt64; fraction/exponent ->
// Float). Tolerated on input: bn="/o/i/" + n="r" splits, "vlo" or "vs" or a
// text "v" for Objlnk, a trailing NUL in an Objlnk, "FFFF:FFFF", base64 with
// either alphabet and optional padding, CBOR half floats and tag-4 times.
//
// An empty payload or pack is an empty read of base: no nodes for the root
// or an object, an empty instance, or an empty multi-instance resource when
// the schema says the resource is Multiple. For a single resource it is an
// error (there is no value).
//
// SenML-ETCH (320/322) also accepts "v": null on a resource instance, which
// Write-Composite uses to delete it (RFC 8790 §3.2, Core §7.5.6): the node
// then has a TypeNone value.
func (c Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	rs, err := c.DecodeRecords(data)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return empty(base, s)
	}
	out := make([]lwm2m.Node, 0, len(rs))
	for _, r := range resolve(rs) {
		p, err := c.path(r.name)
		if err != nil {
			return nil, err
		}
		if !p.IsResource() && !p.IsResourceInstance() {
			return nil, fmt.Errorf("%w: value record on %v", errSenML, p)
		}
		if r.Field == "" {
			return nil, fmt.Errorf("%w: record %v has no value", errSenML, p)
		}
		var v lwm2m.Value
		if r.Field == "v" && r.Value.Type == lwm2m.TypeNone {
			if !c.isETCH() {
				return nil, fmt.Errorf("%w: null value outside SenML-ETCH at %v", errSenML, p)
			}
			if !p.IsResourceInstance() {
				return nil, fmt.Errorf("%w: null (delete) is only for resource instances, got %v", errSenML, p)
			}
		} else {
			t := lwm2m.TypeNone
			if s != nil {
				if def, ok := s.Resource(p.Truncate(3)); ok {
					t = def.Type
				}
			}
			if t == lwm2m.TypeNone && r.Field == "vlo" {
				t = lwm2m.TypeObjlnk
			}
			if v, err = cborc.Coerce(r.Value, t); err != nil {
				return nil, fmt.Errorf("%w: %v: %v", errSenML, p, err)
			}
		}
		n := lwm2m.Node{Path: p, Value: v}
		if r.time != 0 {
			n.Time, n.HasTime = r.time, true
		}
		out = append(out, n)
	}
	return out, nil
}

func empty(base lwm2m.Path, s lwm2m.Schema) ([]lwm2m.Node, error) {
	switch {
	case base.IsRoot(), base.IsObject():
		return nil, nil
	case base.IsInstance():
		return []lwm2m.Node{{Path: base, Kind: lwm2m.KindEmptyInstance}}, nil
	case base.IsResource() && s != nil:
		if def, ok := s.Resource(base); ok && def.Multiple {
			return []lwm2m.Node{{Path: base, Kind: lwm2m.KindEmptyMultiple}}, nil
		}
	}
	return nil, fmt.Errorf("%w: empty payload for %v", errSenML, base)
}

// Encode emits nodes in the spec form described in the package comment.
// Empty-instance and empty-multiple markers have no SenML record and are
// left out (an empty pack is the SenML form of an empty read). A TypeNone
// value is emitted as "v": null in SenML-ETCH (delete) and refused otherwise.
func (c Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	var vals []lwm2m.Node
	for _, n := range nodes {
		if n.Kind == lwm2m.KindValue {
			if !n.Path.HasPrefix(base) {
				return nil, fmt.Errorf("%w: %v is outside %v", errSenML, n.Path, base)
			}
			vals = append(vals, n)
		}
	}
	vals = order(vals)
	whole := true // every node is at base itself: bn is the full path, no n
	for _, n := range vals {
		whole = whole && n.Path == base
	}
	bs := base.String()
	rs := make([]Record, 0, len(vals))
	bt, btSet := 0.0, false
	for i, n := range vals {
		var r Record
		switch {
		case base.IsRoot():
			r.Name = c.RootPath + n.Path.String()
		case whole:
			if i == 0 {
				r.BaseName = c.RootPath + bs
			}
		case n.Path == base:
			return nil, fmt.Errorf("%w: %v has both a value and children", errSenML, base)
		default:
			if i == 0 {
				r.BaseName = c.RootPath + bs + "/"
			}
			r.Name = n.Path.String()[len(bs)+1:]
		}
		want := 0.0 // no time = "now" = 0 (RFC 8428 §4.5.3)
		if n.HasTime {
			want = n.Time
			if !btSet {
				bt, btSet, r.BaseTime = want, true, want
			}
		}
		r.Time = want - bt
		var err error
		if r.Field, r.Value, err = c.field(n.Value); err != nil {
			return nil, fmt.Errorf("%w: %v: %v", errSenML, n.Path, err)
		}
		rs = append(rs, r)
	}
	return c.EncodeRecords(rs)
}

// field maps a typed value to its SenML field (Core Table 7.5.6-1).
func (c Codec) field(v lwm2m.Value) (string, lwm2m.Value, error) {
	switch v.Type {
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return "v", lwm2m.Integer(v.Int), nil
	case lwm2m.TypeUnsigned:
		return "v", lwm2m.Unsigned(v.Uint), nil
	case lwm2m.TypeFloat:
		return "v", lwm2m.Float(v.Float), nil
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return "vs", lwm2m.String(v.Str), nil
	case lwm2m.TypeBoolean:
		return "vb", v, nil
	case lwm2m.TypeOpaque:
		return "vd", v, nil
	case lwm2m.TypeObjlnk:
		return "vlo", lwm2m.String(v.Link.String()), nil
	case lwm2m.TypeNone:
		if c.isETCH() {
			return "v", lwm2m.Value{}, nil
		}
	}
	return "", v, fmt.Errorf("no SenML representation for %v", v.Type)
}

// order keeps each object instance contiguous, in order of first
// appearance, and sorts by path inside it (stable: a time series of one
// resource keeps its order).
func order(ns []lwm2m.Node) []lwm2m.Node {
	idx := map[lwm2m.Path]int{}
	for _, n := range ns {
		if _, ok := idx[n.Path.Truncate(2)]; !ok {
			idx[n.Path.Truncate(2)] = len(idx)
		}
	}
	sort.SliceStable(ns, func(i, j int) bool {
		a, b := idx[ns[i].Path.Truncate(2)], idx[ns[j].Path.Truncate(2)]
		if a != b {
			return a < b
		}
		return ns[i].Path.Compare(ns[j].Path) < 0
	})
	return ns
}

// EncodePaths writes a composite path list (Read-/Observe-Composite FETCH
// body; a Fetch Pack for ETCH, RFC 8790 §3.1): one record per path with the
// full path in "n" (Core Tables 7.5.6-6, -10..-12).
func (c Codec) EncodePaths(paths []lwm2m.Path) ([]byte, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: empty path list", errSenML)
	}
	rs := make([]Record, len(paths))
	for i, p := range paths {
		rs[i].Name = c.RootPath + p.String()
	}
	return c.EncodeRecords(rs)
}

// DecodePaths reads a composite path list. Every record needs n and/or bn
// and no value (Core §6.3.8; RFC 8790 §3.1). Plain SenML also refuses a time
// (a path list carries none); a SenML-ETCH Fetch Pack allows t, bt, u and bu
// and must reject any other field. An empty list is an error (RFC 8790 §3.1:
// at least one record).
func (c Codec) DecodePaths(data []byte) ([]lwm2m.Path, error) {
	rs, err := c.DecodeRecords(data)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("%w: empty path list", errSenML)
	}
	out := make([]lwm2m.Path, 0, len(rs))
	for _, r := range resolve(rs) {
		switch {
		case r.Name == "" && r.BaseName == "":
			return nil, fmt.Errorf("%w: path record without n or bn", errSenML)
		case r.Field != "":
			return nil, fmt.Errorf("%w: path record %q carries a value", errSenML, r.name)
		case !c.isETCH() && (r.Time != 0 || r.BaseTime != 0):
			return nil, fmt.Errorf("%w: path record %q carries a time", errSenML, r.name)
		case c.isETCH() && len(r.Extra) > 0:
			return nil, fmt.Errorf("%w: Fetch Pack record %q has field %q", errSenML, r.name, r.Extra[0])
		}
		p, err := c.path(r.name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// build turns one parsed map (JSON: json.Number numbers, base64 vd; CBOR:
// fxamacker items, byte-string vd) into a Record. Each record carries at most
// one value field; fields ending in "_" must be understood and are refused
// (RFC 8428 §4.4); unknown fields are kept in Extra.
func build(m map[string]any, isCBOR bool) (Record, error) {
	var r Record
	str := func(k string, x any) (string, error) {
		s, ok := x.(string)
		if !ok {
			return "", fmt.Errorf("%w: %q is not a string", errSenML, k)
		}
		return s, nil
	}
	setValue := func(f string, v lwm2m.Value) error {
		if r.Field != "" {
			return fmt.Errorf("%w: record has both %q and %q", errSenML, r.Field, f)
		}
		r.Field, r.Value = f, v
		return nil
	}
	for k, x := range m {
		var err error
		switch k {
		case "bn":
			r.BaseName, err = str(k, x)
		case "n":
			r.Name, err = str(k, x)
		case "bt", "t":
			var v lwm2m.Value
			if v, err = number(x); err == nil {
				f, _ := cborc.AsFloat(v)
				if k == "bt" {
					r.BaseTime = f
				} else {
					r.Time = f
				}
			}
		case "v":
			var v lwm2m.Value
			switch x := x.(type) {
			case nil:
			case string: // a text "v" (Zephyr writes Objlnk so; C7)
				v = lwm2m.String(x)
			default:
				v, err = number(x)
			}
			if err == nil {
				err = setValue(k, v)
			}
		case "vs", "vlo":
			var s string
			if s, err = str(k, x); err == nil {
				err = setValue(k, lwm2m.String(s))
			}
		case "vb":
			b, ok := x.(bool)
			if !ok {
				return r, fmt.Errorf("%w: vb is not a boolean", errSenML)
			}
			err = setValue(k, lwm2m.Boolean(b))
		case "vd":
			var b []byte
			if isCBOR {
				var ok bool
				if b, ok = x.([]byte); !ok {
					return r, fmt.Errorf("%w: vd is not a byte string", errSenML)
				}
			} else {
				var s string
				if s, err = str(k, x); err == nil {
					b, err = decodeBase64(s)
				}
			}
			if err == nil {
				err = setValue(k, lwm2m.Opaque(b))
			}
		case "u", "bu":
		default:
			if strings.HasSuffix(k, "_") {
				return r, fmt.Errorf("%w: unsupported must-understand field %q", errSenML, k)
			}
			r.Extra = append(r.Extra, k)
		}
		if err != nil {
			return r, err
		}
	}
	sort.Strings(r.Extra)
	return r, nil
}

// number reads a JSON number exactly or a CBOR numeric item.
func number(x any) (lwm2m.Value, error) {
	if n, ok := x.(json.Number); ok {
		return jsonNumber(string(n))
	}
	v, err := cborc.Raw(x)
	if err != nil {
		return v, err
	}
	switch v.Type {
	case lwm2m.TypeInteger, lwm2m.TypeUnsigned, lwm2m.TypeFloat:
		return v, nil
	}
	return v, fmt.Errorf("%w: %v is not a number", errSenML, v)
}
