// Package tlv implements the OMA-TLV content format (11542), Core §7.4.4
// (1.2.2 §7.5.5, Table 7.5.5-1) with value encodings from Appendix C,
// Table C-2.
//
// Emit rules (strict):
//   - An Object Instance TLV wraps the resources when the base path has no
//     instance ID (object path). Root paths are refused: TLV cannot carry an
//     object ID.
//   - Every multi-instance resource is a Multiple Resource TLV, also for 0
//     or 1 instances.
//   - Integers, Unsigned and Time use the smallest of 1, 2, 4 or 8 bytes.
//   - Float is 8 bytes, or 4 when Value.Float32 is set (the value came from a
//     32-bit wire field or a 32-bit resource). Table C-2 allows both; 8 bytes
//     never loses precision.
//   - Lengths use bits 2-0 when the value is 0..7 bytes, else the smallest
//     8/16/24-bit length field; IDs above 255 use the 16-bit ID form.
//
// Decode tolerances (Postel, no spec text forbids accepting them):
//   - At an object path, resource TLVs without an Object Instance TLV go to
//     instance 0 (Leshan).
//   - At an instance or resource path, an Object Instance TLV wrapper whose
//     ID matches the path is accepted (it is optional there).
//   - At a resource path, bare Resource Instance TLVs with no Multiple
//     Resource wrapper are accepted (Leshan).
package tlv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

// RecordType is the TLV identifier type, bits 7-6 of the type byte.
type RecordType uint8

const (
	ObjectInstance   RecordType = 0 // 00
	ResourceInstance RecordType = 1 // 01
	MultipleResource RecordType = 2 // 10
	ResourceValue    RecordType = 3 // 11
)

var recordNames = [...]string{"OBJECT_INSTANCE", "RESOURCE_INSTANCE", "MULTIPLE_RESOURCE", "RESOURCE_VALUE"}

func (t RecordType) String() string { return recordNames[t&3] }

// Record is one raw TLV. Object Instance and Multiple Resource records hold
// Children; Resource Instance and Resource records hold Value.
type Record struct {
	Type     RecordType
	ID       uint16
	Value    []byte
	Children []Record
}

func (r Record) container() bool { return r.Type == ObjectInstance || r.Type == MultipleResource }

// maxLen is the largest value a 24-bit length field can carry.
const maxLen = 1<<24 - 1

var errTLV = errors.New("tlv")

func errf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errTLV}, a...)...)
}

// ParseRaw parses a TLV sequence into records. Containers are parsed
// recursively: Object Instance may hold only Resource and Multiple Resource
// TLVs, Multiple Resource only Resource Instance TLVs (Table 7.5.5-1).
func ParseRaw(b []byte) ([]Record, error) { return parse(b, nil) }

// parse reads records whose type must be in allowed (nil = any).
func parse(b []byte, allowed []RecordType) ([]Record, error) {
	var out []Record
	for len(b) > 0 {
		h := b[0]
		r := Record{Type: RecordType(h >> 6)}
		if allowed != nil && !contains(allowed, r.Type) {
			return nil, errf("%v not allowed here", r.Type)
		}
		i := 1
		idLen := 1
		if h&0x20 != 0 {
			idLen = 2
		}
		lenLen := int(h>>3) & 3
		if len(b) < i+idLen+lenLen {
			return nil, errf("truncated header")
		}
		if idLen == 2 {
			r.ID = binary.BigEndian.Uint16(b[i:])
		} else {
			r.ID = uint16(b[i])
		}
		i += idLen
		n := int(h & 7) // bits 2-0 are ignored when a length field follows
		if lenLen > 0 {
			n = 0
			for _, c := range b[i : i+lenLen] {
				n = n<<8 | int(c)
			}
			i += lenLen
		}
		if len(b)-i < n {
			return nil, errf("truncated value: id %d needs %d bytes, %d left", r.ID, n, len(b)-i)
		}
		v := b[i : i+n]
		b = b[i+n:]
		var err error
		switch r.Type {
		case ObjectInstance:
			r.Children, err = parse(v, []RecordType{ResourceValue, MultipleResource})
		case MultipleResource:
			r.Children, err = parse(v, []RecordType{ResourceInstance})
		default:
			r.Value = v
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func contains(ts []RecordType, t RecordType) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// EncodeRaw serialises records (Table 7.5.5-1).
func EncodeRaw(rs []Record) ([]byte, error) {
	var out []byte
	for _, r := range rs {
		v := r.Value
		if r.container() {
			var err error
			if v, err = EncodeRaw(r.Children); err != nil {
				return nil, err
			}
		}
		if len(v) > maxLen {
			return nil, errf("value of %d bytes exceeds the 24-bit length", len(v))
		}
		h := byte(r.Type) << 6
		if r.ID > 255 {
			h |= 0x20
		}
		var lf []byte
		switch n := len(v); {
		case n <= 7:
			h |= byte(n)
		case n <= 0xff:
			h |= 1 << 3
			lf = []byte{byte(n)}
		case n <= 0xffff:
			h |= 2 << 3
			lf = []byte{byte(n >> 8), byte(n)}
		default:
			h |= 3 << 3
			lf = []byte{byte(n >> 16), byte(n >> 8), byte(n)}
		}
		out = append(out, h)
		if r.ID > 255 {
			out = append(out, byte(r.ID>>8))
		}
		out = append(out, byte(r.ID))
		out = append(out, lf...)
		out = append(out, v...)
	}
	return out, nil
}

// EncodeValue encodes a value as a TLV value field (Table C-2).
func EncodeValue(v lwm2m.Value) ([]byte, error) {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return []byte(v.Str), nil
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		i := v.Int
		switch {
		case i >= math.MinInt8 && i <= math.MaxInt8:
			return []byte{byte(i)}, nil
		case i >= math.MinInt16 && i <= math.MaxInt16:
			return binary.BigEndian.AppendUint16(nil, uint16(i)), nil
		case i >= math.MinInt32 && i <= math.MaxInt32:
			return binary.BigEndian.AppendUint32(nil, uint32(i)), nil
		}
		return binary.BigEndian.AppendUint64(nil, uint64(i)), nil
	case lwm2m.TypeUnsigned:
		u := v.Uint
		switch {
		case u <= math.MaxUint8:
			return []byte{byte(u)}, nil
		case u <= math.MaxUint16:
			return binary.BigEndian.AppendUint16(nil, uint16(u)), nil
		case u <= math.MaxUint32:
			return binary.BigEndian.AppendUint32(nil, uint32(u)), nil
		}
		return binary.BigEndian.AppendUint64(nil, u), nil
	case lwm2m.TypeFloat:
		if v.Float32 {
			return binary.BigEndian.AppendUint32(nil, math.Float32bits(float32(v.Float))), nil
		}
		return binary.BigEndian.AppendUint64(nil, math.Float64bits(v.Float)), nil
	case lwm2m.TypeBoolean:
		if v.Bool {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case lwm2m.TypeOpaque:
		return v.Bytes, nil
	case lwm2m.TypeObjlnk:
		return []byte{byte(v.Link.Object >> 8), byte(v.Link.Object), byte(v.Link.Instance >> 8), byte(v.Link.Instance)}, nil
	}
	return nil, errf("cannot encode type %v", v.Type)
}

// DecodeValue decodes a TLV value field as type t (Table C-2). Lengths
// outside the table are rejected: Integer/Time/Unsigned 1, 2, 4 or 8 bytes,
// Float 4 or 8, Boolean exactly 1 byte holding 0 or 1, Objlnk 4.
func DecodeValue(t lwm2m.Type, b []byte) (lwm2m.Value, error) {
	v := lwm2m.Value{Type: t}
	switch t {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		if !utf8.Valid(b) {
			return v, errf("%v is not valid UTF-8", t)
		}
		v.Str = string(b)
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		switch len(b) {
		case 1:
			v.Int = int64(int8(b[0]))
		case 2:
			v.Int = int64(int16(binary.BigEndian.Uint16(b)))
		case 4:
			v.Int = int64(int32(binary.BigEndian.Uint32(b)))
		case 8:
			v.Int = int64(binary.BigEndian.Uint64(b))
		default:
			return v, errf("%v of %d bytes", t, len(b))
		}
	case lwm2m.TypeUnsigned:
		switch len(b) {
		case 1:
			v.Uint = uint64(b[0])
		case 2:
			v.Uint = uint64(binary.BigEndian.Uint16(b))
		case 4:
			v.Uint = uint64(binary.BigEndian.Uint32(b))
		case 8:
			v.Uint = binary.BigEndian.Uint64(b)
		default:
			return v, errf("unsigned of %d bytes", len(b))
		}
	case lwm2m.TypeFloat:
		switch len(b) {
		case 4:
			v.Float = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
			v.Float32 = true
		case 8:
			v.Float = math.Float64frombits(binary.BigEndian.Uint64(b))
		default:
			return v, errf("float of %d bytes", len(b))
		}
	case lwm2m.TypeBoolean:
		if len(b) != 1 || b[0] > 1 {
			return v, errf("boolean must be one byte 0 or 1, got %x", b)
		}
		v.Bool = b[0] == 1
	case lwm2m.TypeOpaque:
		v.Bytes = append([]byte{}, b...)
	case lwm2m.TypeObjlnk:
		if len(b) != 4 {
			return v, errf("objlnk of %d bytes", len(b))
		}
		v.Link = lwm2m.ObjLink{Object: binary.BigEndian.Uint16(b), Instance: binary.BigEndian.Uint16(b[2:])}
	default:
		return v, errf("cannot decode type %v", t)
	}
	return v, nil
}

// Codec is the TLV codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatTLV }

// Decode decodes a TLV payload addressed to base. Resource types come from s;
// unknown resources decode as opaque. An empty payload is an object with no
// instances, an empty instance, or an empty multi-instance resource, and is
// an error for a single-instance resource or a resource instance.
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	if base.IsRoot() {
		return nil, errf("cannot decode at the root path")
	}
	recs, err := ParseRaw(data)
	if err != nil {
		return nil, err
	}
	d := decoder{s: s}
	if base.IsObject() {
		err = d.object(base, recs)
	} else {
		if len(recs) == 1 && recs[0].Type == ObjectInstance { // optional OI wrapper
			if recs[0].ID != base.Instance() {
				return nil, errf("object instance TLV %d does not match path %v", recs[0].ID, base)
			}
			recs = recs[0].Children
		}
		inst := base.Truncate(2)
		switch {
		case len(recs) == 0:
			err = d.empty(base)
		case base.Len() >= 3 && allType(recs, ResourceInstance):
			err = d.multiple(base.Truncate(3), recs)
		default:
			err = d.instance(inst, recs)
		}
	}
	if err != nil {
		return nil, err
	}
	for _, n := range d.out {
		if !n.Path.HasPrefix(base) {
			return nil, errf("%v is outside the request path %v", n.Path, base)
		}
	}
	return d.out, nil
}

func allType(rs []Record, t RecordType) bool {
	for _, r := range rs {
		if r.Type != t {
			return false
		}
	}
	return true
}

type decoder struct {
	s   lwm2m.Schema
	out []lwm2m.Node
}

func (d *decoder) def(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
	if d.s == nil {
		return lwm2m.ResourceDef{}, false
	}
	return d.s.Resource(p)
}

func (d *decoder) empty(base lwm2m.Path) error {
	switch base.Len() {
	case 2:
		d.out = append(d.out, lwm2m.Node{Path: base, Kind: lwm2m.KindEmptyInstance})
		return nil
	case 3:
		if def, ok := d.def(base); ok && def.Multiple {
			d.out = append(d.out, lwm2m.Node{Path: base, Kind: lwm2m.KindEmptyMultiple})
			return nil
		}
	}
	return errf("empty payload for %v", base)
}

func unique(rs []Record) error {
	seen := map[uint16]bool{}
	for _, r := range rs {
		if seen[r.ID] {
			return errf("duplicate id %d", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

func (d *decoder) object(obj lwm2m.Path, recs []Record) error {
	var bare []Record
	var insts []Record
	for _, r := range recs {
		if r.Type == ObjectInstance {
			insts = append(insts, r)
		} else {
			bare = append(bare, r)
		}
	}
	if len(bare) > 0 {
		// ponytail: tolerance, unwrapped resources at an object path are instance 0 (Leshan).
		if len(insts) > 0 {
			return errf("mixed object instance and resource TLVs at %v", obj)
		}
		insts = []Record{{Type: ObjectInstance, ID: 0, Children: bare}}
	}
	if err := unique(insts); err != nil {
		return err
	}
	for _, oi := range insts {
		p := obj.Append(oi.ID)
		if len(oi.Children) == 0 {
			d.out = append(d.out, lwm2m.Node{Path: p, Kind: lwm2m.KindEmptyInstance})
			continue
		}
		if err := d.instance(p, oi.Children); err != nil {
			return err
		}
	}
	return nil
}

func (d *decoder) instance(inst lwm2m.Path, recs []Record) error {
	if err := unique(recs); err != nil {
		return err
	}
	for _, r := range recs {
		p := inst.Append(r.ID)
		def, ok := d.def(p)
		switch r.Type {
		case ResourceValue:
			if ok && def.Multiple {
				return errf("%v is multi-instance but sent as a single resource TLV", p)
			}
			v, err := DecodeValue(typeOf(def, ok), r.Value)
			if err != nil {
				return fmt.Errorf("%v: %w", p, err)
			}
			d.out = append(d.out, lwm2m.ValueNode(p, v))
		case MultipleResource:
			if ok && !def.Multiple {
				return errf("%v is single-instance but sent as a multiple resource TLV", p)
			}
			if len(r.Children) == 0 {
				d.out = append(d.out, lwm2m.Node{Path: p, Kind: lwm2m.KindEmptyMultiple})
				continue
			}
			if err := d.multiple(p, r.Children); err != nil {
				return err
			}
		default:
			return errf("%v TLV not allowed in an object instance", r.Type)
		}
	}
	return nil
}

func (d *decoder) multiple(res lwm2m.Path, recs []Record) error {
	if err := unique(recs); err != nil {
		return err
	}
	def, ok := d.def(res)
	if ok && !def.Multiple {
		return errf("%v is single-instance but sent as resource instances", res)
	}
	t := typeOf(def, ok)
	for _, r := range recs {
		p := res.Append(r.ID)
		v, err := DecodeValue(t, r.Value)
		if err != nil {
			return fmt.Errorf("%v: %w", p, err)
		}
		d.out = append(d.out, lwm2m.ValueNode(p, v))
	}
	return nil
}

// typeOf returns the schema type, or opaque for an unknown resource.
func typeOf(def lwm2m.ResourceDef, ok bool) lwm2m.Type {
	if !ok || def.Type == lwm2m.TypeNone {
		return lwm2m.TypeOpaque
	}
	return def.Type
}

// Encode encodes nodes below base (see the package doc for the rules).
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	if base.IsRoot() {
		return nil, errf("cannot encode at the root path")
	}
	ns := append([]lwm2m.Node(nil), nodes...)
	lwm2m.SortNodes(ns)
	for i, n := range ns {
		if !n.Path.HasPrefix(base) {
			return nil, errf("%v is outside the request path %v", n.Path, base)
		}
		if i > 0 && ns[i-1].Path == n.Path {
			return nil, errf("duplicate node %v", n.Path)
		}
		want := 3
		switch n.Kind {
		case lwm2m.KindEmptyInstance:
			want = 2
		case lwm2m.KindValue:
			if n.Path.Len() == 4 {
				want = 4
			}
		}
		if n.Path.Len() != want {
			return nil, errf("invalid node %v", n)
		}
	}
	// Build the record tree top-down: object instances, then resources.
	var insts []Record
	for _, n := range ns {
		if len(insts) == 0 || insts[len(insts)-1].ID != n.Path.Instance() {
			insts = append(insts, Record{Type: ObjectInstance, ID: n.Path.Instance()})
		}
		oi := &insts[len(insts)-1]
		if n.Kind == lwm2m.KindEmptyInstance {
			if len(ns) > 1 && base.Len() >= 2 {
				return nil, errf("empty instance %v mixed with other nodes", n.Path)
			}
			continue
		}
		rid := n.Path.Resource()
		if n.Kind == lwm2m.KindEmptyMultiple || n.Path.Len() == 4 {
			k := len(oi.Children) - 1
			if k < 0 || oi.Children[k].ID != rid {
				oi.Children = append(oi.Children, Record{Type: MultipleResource, ID: rid})
				k++
			} else if oi.Children[k].Type != MultipleResource {
				return nil, errf("%v is both single and multi-instance", n.Path.Truncate(3))
			}
			if n.Kind == lwm2m.KindValue {
				v, err := EncodeValue(n.Value)
				if err != nil {
					return nil, err
				}
				oi.Children[k].Children = append(oi.Children[k].Children, Record{Type: ResourceInstance, ID: n.Path.ResourceInstance(), Value: v})
			}
			continue
		}
		if k := len(oi.Children) - 1; k >= 0 && oi.Children[k].ID == rid {
			return nil, errf("%v is both single and multi-instance", n.Path)
		}
		v, err := EncodeValue(n.Value)
		if err != nil {
			return nil, err
		}
		oi.Children = append(oi.Children, Record{Type: ResourceValue, ID: rid, Value: v})
	}
	var top []Record
	switch base.Len() {
	case 1:
		top = insts
	case 2, 3:
		if len(insts) > 0 {
			top = insts[0].Children
		}
	case 4:
		if len(insts) == 0 {
			return nil, errf("no value for %v", base)
		}
		top = insts[0].Children[0].Children
	}
	if base.Len() == 3 && len(top) == 0 && len(ns) == 0 {
		return nil, errf("no value for %v", base)
	}
	return EncodeRaw(top)
}
