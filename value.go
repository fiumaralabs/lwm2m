package lwm2m

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Type is an LwM2M resource data type (Core Appendix C, Table C-1).
type Type uint8

const (
	TypeNone     Type = iota // executable or unknown
	TypeString               // UTF-8
	TypeInteger              // signed 64-bit
	TypeUnsigned             // unsigned 64-bit (1.1+)
	TypeFloat                // IEEE 754, 32 or 64-bit
	TypeBoolean
	TypeOpaque
	TypeTime    // signed Unix seconds
	TypeObjlnk  // object link oid:iid
	TypeCorelnk // CoRE link-format string (1.1+)
)

var typeNames = [...]string{"none", "string", "integer", "unsigned", "float", "boolean", "opaque", "time", "objlnk", "corelnk"}

func (t Type) String() string {
	if int(t) < len(typeNames) {
		return typeNames[t]
	}
	return "Type(" + strconv.Itoa(int(t)) + ")"
}

// ParseType accepts the names used in the vectors and the OMA object XML
// (case-insensitive): "string", "integer", "unsigned integer", "float",
// "boolean", "opaque", "time", "objlnk", "corelnk", "none".
func ParseType(s string) (Type, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return TypeNone, nil
	case "string":
		return TypeString, nil
	case "integer":
		return TypeInteger, nil
	case "unsigned", "unsigned integer", "unsignedinteger":
		return TypeUnsigned, nil
	case "float":
		return TypeFloat, nil
	case "boolean":
		return TypeBoolean, nil
	case "opaque":
		return TypeOpaque, nil
	case "time":
		return TypeTime, nil
	case "objlnk":
		return TypeObjlnk, nil
	case "corelnk":
		return TypeCorelnk, nil
	}
	return TypeNone, fmt.Errorf("lwm2m: unknown type %q", s)
}

// ObjLink is an Objlnk value. 65535:65535 is the null link.
type ObjLink struct{ Object, Instance uint16 }

var NullObjLink = ObjLink{MaxID, MaxID}

func (l ObjLink) String() string { return fmt.Sprintf("%d:%d", l.Object, l.Instance) }

// ParseObjLink parses the strict "oid:iid" text form: two decimal numbers
// 0..65535, no sign, no whitespace (Core Appendix C).
func ParseObjLink(s string) (ObjLink, error) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return ObjLink{}, fmt.Errorf("lwm2m: objlnk %q: missing ':'", s)
	}
	o, err1 := parseU16(a)
	i, err2 := parseU16(b)
	if err1 != nil || err2 != nil {
		return ObjLink{}, fmt.Errorf("lwm2m: objlnk %q: invalid number", s)
	}
	return ObjLink{o, i}, nil
}

func parseU16(s string) (uint16, error) {
	if s == "" || len(s) > 5 {
		return 0, fmt.Errorf("bad")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("bad")
		}
	}
	v, err := strconv.ParseUint(s, 10, 16)
	return uint16(v), err
}

// Value is a typed LwM2M value. Exactly one field matches Type.
type Value struct {
	Type    Type
	Str     string  // String, Corelnk
	Int     int64   // Integer, Time
	Uint    uint64  // Unsigned
	Float   float64 // Float
	Bool    bool    // Boolean
	Bytes   []byte  // Opaque
	Link    ObjLink // Objlnk
	Float32 bool    // Float: the wire value was 32-bit (informational)
}

func String(s string) Value    { return Value{Type: TypeString, Str: s} }
func Integer(i int64) Value    { return Value{Type: TypeInteger, Int: i} }
func Unsigned(u uint64) Value  { return Value{Type: TypeUnsigned, Uint: u} }
func Float(f float64) Value    { return Value{Type: TypeFloat, Float: f} }
func Boolean(b bool) Value     { return Value{Type: TypeBoolean, Bool: b} }
func Opaque(b []byte) Value    { return Value{Type: TypeOpaque, Bytes: b} }
func Time(t int64) Value       { return Value{Type: TypeTime, Int: t} }
func TimeOf(t time.Time) Value { return Time(t.Unix()) }
func Objlnk(o, i uint16) Value { return Value{Type: TypeObjlnk, Link: ObjLink{o, i}} }
func Corelnk(s string) Value   { return Value{Type: TypeCorelnk, Str: s} }

// Equal compares type and payload. Floats compare by value (NaN equals NaN).
func (v Value) Equal(w Value) bool {
	if v.Type != w.Type {
		return false
	}
	switch v.Type {
	case TypeString, TypeCorelnk:
		return v.Str == w.Str
	case TypeInteger, TypeTime:
		return v.Int == w.Int
	case TypeUnsigned:
		return v.Uint == w.Uint
	case TypeFloat:
		return v.Float == w.Float || (math.IsNaN(v.Float) && math.IsNaN(w.Float))
	case TypeBoolean:
		return v.Bool == w.Bool
	case TypeOpaque:
		return bytes.Equal(v.Bytes, w.Bytes)
	case TypeObjlnk:
		return v.Link == w.Link
	}
	return true
}

func (v Value) String() string {
	switch v.Type {
	case TypeString, TypeCorelnk:
		return strconv.Quote(v.Str)
	case TypeInteger, TypeTime:
		return strconv.FormatInt(v.Int, 10)
	case TypeUnsigned:
		return strconv.FormatUint(v.Uint, 10)
	case TypeFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case TypeBoolean:
		return strconv.FormatBool(v.Bool)
	case TypeOpaque:
		return fmt.Sprintf("0x%x", v.Bytes)
	case TypeObjlnk:
		return v.Link.String()
	}
	return "none"
}
