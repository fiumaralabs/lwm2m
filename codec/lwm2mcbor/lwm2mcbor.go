// Package lwm2mcbor implements LwM2M CBOR, application/vnd.oma.lwm2m+cbor,
// Content-Format 11544 (Core 1.2.2 §7.5.4, Appendix C Table C-2), with the
// Gateway prefix extension on input (Gateway TS 1.1.1 §9).
package lwm2mcbor

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

// Codec is the LwM2M CBOR codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatLwM2MCBOR }

var (
	errFormat = errors.New("lwm2mcbor: invalid payload")
	// ErrPrefix: the payload carries Gateway end-device prefixes, which
	// lwm2m.Node cannot hold. Use DecodePrefixed.
	ErrPrefix = errors.New("lwm2mcbor: gateway prefix in payload, use DecodePrefixed")
)

func fail(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errFormat, fmt.Sprintf(format, a...))
}

var (
	dm, _ = cbor.DecOptions{}.DecMode() // rejects invalid UTF-8
	em, _ = cbor.EncOptions{ShortestFloat: cbor.ShortestFloat16}.EncMode()
)

// PrefixedNode is a node of a Gateway end device ("" = the gateway itself).
type PrefixedNode struct {
	Prefix string
	lwm2m.Node
}

// Decode accepts every layout of the §7.5.4 grammar: definite or indefinite
// maps and array keys, uint or array keys, nesting at any depth, mixed in
// one payload. It rejects duplicate paths (A-8) and paths outside base.
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	pns, err := DecodePrefixed(base, data, s)
	if err != nil {
		return nil, err
	}
	out := make([]lwm2m.Node, len(pns))
	for i, pn := range pns {
		if pn.Prefix != "" {
			return nil, ErrPrefix
		}
		out[i] = pn.Node
	}
	return out, nil
}

// DecodePrefixed is Decode that also accepts a text PREFIX as a top-level key
// or as the first element of a top-level array key (Gateway TS §9). base and
// s apply to the path inside the end device.
func DecodePrefixed(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]PrefixedNode, error) {
	d := &decoder{base: base, s: s, seen: map[string]lwm2m.Kind{}}
	rest, err := d.mapValue(data, "", nil, true)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fail("%d trailing bytes", len(rest))
	}
	for _, n := range d.out {
		for p := n.Path.Parent(); p.Len() >= 2; p = p.Parent() {
			if _, ok := d.seen[n.Prefix+p.String()]; ok {
				return nil, fail("%s conflicts with %s", n.Path, p)
			}
		}
	}
	return d.out, nil
}

type decoder struct {
	base lwm2m.Path
	s    lwm2m.Schema
	seen map[string]lwm2m.Kind
	out  []PrefixedNode
}

var errTrunc = fail("truncated")

// head reads an item head: major type, additional info and argument.
func head(b []byte) (major, info byte, arg uint64, rest []byte, err error) {
	if len(b) == 0 {
		return 0, 0, 0, nil, errTrunc
	}
	major, info, b = b[0]>>5, b[0]&0x1f, b[1:]
	switch {
	case info < 24:
		arg = uint64(info)
	case info <= 27:
		n := 1 << (info - 24)
		if len(b) < n {
			return 0, 0, 0, nil, errTrunc
		}
		for _, c := range b[:n] {
			arg = arg<<8 | uint64(c)
		}
		b = b[n:]
	case info == 31:
	default:
		return 0, 0, 0, nil, fail("reserved additional info %d", info)
	}
	return major, info, arg, b, nil
}

// items iterates the elements of a definite (n) or indefinite array/map.
func items(b []byte, info byte, n uint64, each func([]byte) ([]byte, error)) ([]byte, int, error) {
	count := 0
	for i := uint64(0); info == 31 || i < n; i++ {
		if info == 31 {
			if len(b) == 0 {
				return nil, 0, errTrunc
			}
			if b[0] == 0xff {
				return b[1:], count, nil
			}
		}
		var err error
		if b, err = each(b); err != nil {
			return nil, 0, err
		}
		count++
	}
	return b, count, nil
}

func (d *decoder) mapValue(b []byte, prefix string, ids []uint16, top bool) ([]byte, error) {
	major, info, n, b, err := head(b)
	if err != nil {
		return nil, err
	}
	if major != 5 {
		return nil, fail("expected a map, got major type %d", major)
	}
	if len(ids) == 4 {
		return nil, fail("map below a resource instance")
	}
	b, count, err := items(b, info, n, func(b []byte) ([]byte, error) {
		pre, kids, b, err := key(b, top && prefix == "")
		if err != nil {
			return nil, err
		}
		p := append(append([]uint16(nil), ids...), kids...)
		if len(p) > 4 {
			return nil, fail("path deeper than 4 levels")
		}
		if len(b) > 0 && b[0]>>5 == 5 {
			return d.mapValue(b, prefix+pre, p, top && len(p) == 0)
		}
		return d.leaf(b, prefix+pre, p)
	})
	if err != nil {
		return nil, err
	}
	if count == 0 {
		// Grammar says 1*(ID, VALUE); tolerate empty maps as empty containers
		// (an empty read answers bfff in Anjay Lite).
		switch len(ids) {
		case 2:
			return b, d.add(prefix, lwm2m.Node{Path: lwm2m.NewPath(ids...), Kind: lwm2m.KindEmptyInstance})
		case 3:
			return b, d.add(prefix, lwm2m.Node{Path: lwm2m.NewPath(ids...), Kind: lwm2m.KindEmptyMultiple})
		}
	}
	return b, nil
}

// key reads an ID: uint, or an array of uints. With prefixOK a text string
// may be the key or the first array element (Gateway PREFIX).
func key(b []byte, prefixOK bool) (prefix string, ids []uint16, rest []byte, err error) {
	if len(b) == 0 {
		return "", nil, nil, errTrunc
	}
	id := func(b []byte) ([]byte, error) {
		major, info, arg, rest, err := head(b)
		switch {
		case err != nil:
			return nil, err
		case major != 0 || info == 31:
			return nil, fail("ID must be a definite unsigned integer (major type %d, info %d)", major, info)
		case arg > lwm2m.MaxID:
			return nil, fail("ID %d > 65535", arg)
		}
		ids = append(ids, uint16(arg))
		return rest, nil
	}
	text := func(b []byte) ([]byte, error) {
		rest, err := dm.UnmarshalFirst(b, &prefix)
		if err == nil && prefix == "" {
			err = fail("empty prefix")
		}
		return rest, err
	}
	switch b[0] >> 5 {
	case 0:
		rest, err = id(b)
		return prefix, ids, rest, err
	case 3:
		if !prefixOK {
			return "", nil, nil, fail("text key below the top level")
		}
		rest, err = text(b)
		return prefix, nil, rest, err
	case 4:
		_, info, n, b, err := head(b)
		if err != nil {
			return "", nil, nil, err
		}
		first := true
		rest, count, err := items(b, info, n, func(b []byte) ([]byte, error) {
			defer func() { first = false }()
			if first && prefixOK && len(b) > 0 && b[0]>>5 == 3 {
				return text(b)
			}
			return id(b)
		})
		if err == nil && len(ids) == 0 && (count == 0 || prefix == "") {
			err = fail("empty array key")
		}
		return prefix, ids, rest, err
	}
	return "", nil, nil, fail("ID must be uint or array, got major type %d", b[0]>>5)
}

func (d *decoder) add(prefix string, n lwm2m.Node) error {
	if !n.Path.HasPrefix(d.base) {
		return fail("%s is outside the request path %s", n.Path, d.base)
	}
	k := prefix + n.Path.String()
	if _, dup := d.seen[k]; dup {
		return fail("duplicate path %s", n.Path)
	}
	d.seen[k] = n.Kind
	d.out = append(d.out, PrefixedNode{prefix, n})
	return nil
}

type epoch int64 // a decoded Time candidate

func (d *decoder) leaf(b []byte, prefix string, ids []uint16) ([]byte, error) {
	if len(ids) < 3 {
		return nil, fail("value at %v: values sit at resource or resource-instance level", ids)
	}
	p := lwm2m.NewPath(ids...)
	var raw cbor.RawMessage
	rest, err := dm.UnmarshalFirst(b, &raw)
	if err != nil {
		return nil, fail("%s: %v", p, err)
	}
	x, err := item(raw)
	if err != nil {
		return nil, fail("%s: %v", p, err)
	}
	var def lwm2m.ResourceDef
	known := false
	if d.s != nil {
		def, known = d.s.Resource(p.Truncate(3))
	}
	if known && def.Multiple != (p.Len() == 4) {
		return nil, fail("%s: multiple=%v in the model", p, def.Multiple)
	}
	v, err := cast(x, def.Type, known)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	v.Float32 = raw[0] == 0xfa
	return rest, d.add(prefix, lwm2m.ValueNode(p, v))
}

// item decodes one well-formed leaf into uint64, int64, float64, bool, nil,
// []byte, string or epoch (tag 1 number, tag 0 RFC 3339 string).
func item(raw []byte) (any, error) {
	var err error
	switch raw[0] >> 5 {
	case 0:
		var u uint64
		err = dm.Unmarshal(raw, &u)
		return u, err
	case 1:
		var i int64
		if err = dm.Unmarshal(raw, &i); err != nil {
			return nil, fail("integer out of int64 range")
		}
		return i, nil
	case 2:
		var bs []byte
		err = dm.Unmarshal(raw, &bs)
		return bs, err
	case 3:
		var s string
		err = dm.Unmarshal(raw, &s)
		return s, err
	case 6:
		var tag cbor.RawTag
		if err = dm.Unmarshal(raw, &tag); err != nil {
			return nil, err
		}
		x, err := item(tag.Content)
		if err != nil {
			return nil, err
		}
		switch t := x.(type) {
		case uint64:
			if tag.Number == 1 && t <= math.MaxInt64 {
				return epoch(t), nil
			}
		case int64:
			if tag.Number == 1 {
				return epoch(t), nil
			}
		case float64:
			if tag.Number == 1 && !math.IsNaN(t) && math.Abs(t) < 1<<63 {
				return epoch(math.Floor(t)), nil
			}
		case string:
			if tag.Number == 0 { // C §7.5.3 / RFC 8949 §3.4.1; Zephyr CBOR (C7)
				tm, err := time.Parse(time.RFC3339, t)
				if err != nil {
					return nil, fail("tag 0: %v", err)
				}
				return epoch(tm.Unix()), nil
			}
		}
		return nil, fail("unsupported tag %d", tag.Number)
	case 7:
		switch raw[0] {
		case 0xf4, 0xf5:
			return raw[0] == 0xf5, nil
		case 0xf6:
			return nil, nil
		case 0xf9, 0xfa, 0xfb:
			var f float64
			err = dm.Unmarshal(raw, &f)
			return f, err
		}
		return nil, fail("unsupported simple value 0x%x", raw[0])
	}
	return nil, fail("major type %d is not a value", raw[0]>>5)
}

// cast types x by the model (Table C-2), or infers a type when unknown.
func cast(x any, t lwm2m.Type, known bool) (lwm2m.Value, error) {
	if !known {
		switch v := x.(type) {
		case uint64:
			if v > math.MaxInt64 {
				return lwm2m.Unsigned(v), nil
			}
			return lwm2m.Integer(int64(v)), nil
		case int64:
			return lwm2m.Integer(v), nil
		case float64:
			return lwm2m.Float(v), nil
		case bool:
			return lwm2m.Boolean(v), nil
		case []byte:
			return lwm2m.Opaque(v), nil
		case string:
			return lwm2m.String(v), nil
		case epoch:
			return lwm2m.Time(int64(v)), nil
		}
		return lwm2m.Value{}, nil // null: none
	}
	ok := false
	var out lwm2m.Value
	switch v := x.(type) {
	case uint64:
		switch {
		case t == lwm2m.TypeUnsigned:
			out, ok = lwm2m.Unsigned(v), true
		case (t == lwm2m.TypeInteger || t == lwm2m.TypeTime) && v <= math.MaxInt64:
			out, ok = lwm2m.Value{Type: t, Int: int64(v)}, true
		}
	case int64:
		if t == lwm2m.TypeInteger || t == lwm2m.TypeTime {
			out, ok = lwm2m.Value{Type: t, Int: v}, true
		}
	case epoch:
		out, ok = lwm2m.Time(int64(v)), t == lwm2m.TypeTime
	case float64:
		out, ok = lwm2m.Float(v), t == lwm2m.TypeFloat
	case bool:
		out, ok = lwm2m.Boolean(v), t == lwm2m.TypeBoolean
	case []byte:
		out, ok = lwm2m.Opaque(v), t == lwm2m.TypeOpaque
	case string:
		switch t {
		case lwm2m.TypeString, lwm2m.TypeCorelnk:
			out, ok = lwm2m.Value{Type: t, Str: v}, true
		case lwm2m.TypeObjlnk:
			l, err := lwm2m.ParseObjLink(v)
			if err != nil {
				return out, fail("%v", err)
			}
			out, ok = lwm2m.Value{Type: t, Link: l}, true
		}
	case nil:
		ok = t == lwm2m.TypeNone // A-8: null only for "none"
	}
	if !ok {
		return lwm2m.Value{}, fail("%T value for a %v resource", x, t)
	}
	return out, nil
}

// Encode writes definite-length maps. Each top-level key is the path from
// the object down to the first branch, a uint for one ID or an array key
// otherwise; below it one map level per ID. Keys keep the order of nodes.
// This reproduces the Core §7.5.4.1-§7.5.4.5 examples byte for byte. Values
// use preferred serialization (shortest exact float), Time as a plain
// integer, Objlnk as "oid:iid", none as null. An empty instance or
// multi-instance resource is written as an empty map.
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	if len(nodes) == 0 {
		return nil, fail("nothing to encode (the grammar requires at least one entry)")
	}
	root := &tnode{}
	for _, n := range nodes {
		if !n.Path.HasPrefix(base) {
			return nil, fail("%s is outside %s", n.Path, base)
		}
		if n.HasTime {
			return nil, fail("%s: LwM2M CBOR has no timestamps", n.Path)
		}
		want := map[lwm2m.Kind][2]int{lwm2m.KindValue: {3, 4}, lwm2m.KindEmptyInstance: {2, 2}, lwm2m.KindEmptyMultiple: {3, 3}}[n.Kind]
		if l := n.Path.Len(); l < want[0] || l > want[1] {
			return nil, fail("%s: wrong level for node kind %d", n.Path, n.Kind)
		}
		cur := root
		for i := 0; i < n.Path.Len(); i++ {
			if cur.set {
				return nil, fail("%s conflicts with another node", n.Path)
			}
			cur = cur.child(n.Path.ID(i))
		}
		if cur.set || len(cur.kids) > 0 {
			return nil, fail("%s given twice or conflicting", n.Path)
		}
		cur.set = true
		if n.Kind == lwm2m.KindValue {
			v := n.Value
			cur.leaf = &v
		}
	}
	var buf bytes.Buffer
	writeHead(&buf, 5, uint64(len(root.kids)))
	for _, k := range root.kids {
		ids := []uint16{k.id}
		for !k.set && len(k.kids) == 1 {
			k = k.kids[0]
			ids = append(ids, k.id)
		}
		if len(ids) > 1 {
			writeHead(&buf, 4, uint64(len(ids)))
		}
		for _, id := range ids {
			writeHead(&buf, 0, uint64(id))
		}
		if err := k.write(&buf); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

type tnode struct {
	id   uint16
	kids []*tnode
	set  bool         // a node ends here
	leaf *lwm2m.Value // nil with set: empty container
}

func (t *tnode) child(id uint16) *tnode {
	for _, k := range t.kids {
		if k.id == id {
			return k
		}
	}
	k := &tnode{id: id}
	t.kids = append(t.kids, k)
	return k
}

func (t *tnode) write(buf *bytes.Buffer) error {
	if t.leaf != nil {
		b, err := em.Marshal(goValue(*t.leaf))
		buf.Write(b)
		return err
	}
	writeHead(buf, 5, uint64(len(t.kids)))
	for _, k := range t.kids {
		writeHead(buf, 0, uint64(k.id))
		if err := k.write(buf); err != nil {
			return err
		}
	}
	return nil
}

func goValue(v lwm2m.Value) any {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return v.Int
	case lwm2m.TypeUnsigned:
		return v.Uint
	case lwm2m.TypeFloat:
		return v.Float
	case lwm2m.TypeBoolean:
		return v.Bool
	case lwm2m.TypeOpaque:
		return append([]byte{}, v.Bytes...) // non-nil: nil would encode as null
	case lwm2m.TypeObjlnk:
		return v.Link.String()
	}
	return nil
}

func writeHead(buf *bytes.Buffer, major byte, n uint64) {
	m := major << 5
	switch {
	case n < 24:
		buf.WriteByte(m | byte(n))
	case n <= math.MaxUint8:
		buf.Write([]byte{m | 24, byte(n)})
	case n <= math.MaxUint16:
		buf.Write([]byte{m | 25, byte(n >> 8), byte(n)})
	case n <= math.MaxUint32:
		buf.Write([]byte{m | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
	default:
		buf.WriteByte(m | 27)
		for i := 56; i >= 0; i -= 8 {
			buf.WriteByte(byte(n >> i))
		}
	}
}
