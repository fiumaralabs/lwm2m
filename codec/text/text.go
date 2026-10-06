// Package text implements the plain-text content format (0), Core §7.4.1
// (1.2.2 §7.5.1) with the value encodings of Appendix C, Table C-2, plus the
// Execute-argument syntax of Core §6.3.5.
//
// Plain text carries exactly one value of a single-instance resource or of a
// resource instance.
//
// Emit rules: Integer, Unsigned and Time as ASCII decimal; Float as plain
// decimal with no exponent (Table C-2 example 6.667e-11 -> "0.00000000006667"),
// the shortest digits that round-trip, and ".0" appended to whole numbers so
// the text still reads as a float (as Leshan and Zephyr write it); Boolean
// "0"/"1"; Opaque as padded standard base64 (RFC 4648 §4); Objlnk "oid:iid".
// NaN and infinities have no plain-decimal form and are refused.
//
// Decode: Float also accepts an exponent ("1.0E10", Leshan). Booleans accept
// only "0" and "1" (the spec values; "true"/"false" are refused).
package text

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

var errText = errors.New("text")

func errf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errText}, a...)...)
}

// Codec is the plain-text codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatText }

func checkBase(base lwm2m.Path, s lwm2m.Schema) (lwm2m.ResourceDef, bool, error) {
	if !base.IsResource() && !base.IsResourceInstance() {
		return lwm2m.ResourceDef{}, false, errf("plain text needs a resource or resource-instance path, got %v", base)
	}
	if s == nil {
		return lwm2m.ResourceDef{}, false, nil
	}
	def, ok := s.Resource(base.Truncate(3))
	if ok && def.Multiple && base.IsResource() {
		return def, ok, errf("%v is multi-instance: plain text carries a single value", base)
	}
	return def, ok, nil
}

// Decode decodes a single value at base. The type comes from s; an unknown
// resource decodes as a string.
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	def, ok, err := checkBase(base, s)
	if err != nil {
		return nil, err
	}
	t := lwm2m.TypeString
	if ok {
		t = def.Type
	}
	v, err := ParseValue(t, string(data))
	if err != nil {
		return nil, err
	}
	return []lwm2m.Node{lwm2m.ValueNode(base, v)}, nil
}

// Encode encodes the single node at base.
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	if _, _, err := checkBase(base, nil); err != nil {
		return nil, err
	}
	if len(nodes) != 1 || nodes[0].Kind != lwm2m.KindValue || nodes[0].Path != base {
		return nil, errf("plain text needs exactly one value at %v", base)
	}
	s, err := FormatValue(nodes[0].Value)
	return []byte(s), err
}

// ParseValue parses the plain-text form of a value of type t (Table C-2).
func ParseValue(t lwm2m.Type, s string) (lwm2m.Value, error) {
	v := lwm2m.Value{Type: t}
	var err error
	switch t {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		if !utf8.ValidString(s) {
			return v, errf("%v is not valid UTF-8", t)
		}
		v.Str = s
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		v.Int, err = strconv.ParseInt(s, 10, 64)
	case lwm2m.TypeUnsigned:
		v.Uint, err = strconv.ParseUint(s, 10, 64)
	case lwm2m.TypeFloat:
		// ParseFloat also takes "inf", "NaN", hex and underscores; only decimals are allowed.
		if strings.Trim(s, "0123456789+-.eE") != "" {
			return v, errf("float %q is not a decimal number", s)
		}
		v.Float, err = strconv.ParseFloat(s, 64)
	case lwm2m.TypeBoolean:
		switch s {
		case "0":
		case "1":
			v.Bool = true
		default:
			return v, errf("boolean must be \"0\" or \"1\", got %q", s)
		}
	case lwm2m.TypeOpaque:
		v.Bytes, err = base64.StdEncoding.Strict().DecodeString(s)
		if v.Bytes == nil {
			v.Bytes = []byte{}
		}
	case lwm2m.TypeObjlnk:
		v.Link, err = lwm2m.ParseObjLink(s)
	default:
		return v, errf("type %v has no plain-text form", t)
	}
	if err != nil {
		return v, errf("%v %q: %v", t, s, err)
	}
	return v, nil
}

// FormatValue renders v in plain text (Table C-2).
func FormatValue(v lwm2m.Value) (string, error) {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str, nil
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return strconv.FormatInt(v.Int, 10), nil
	case lwm2m.TypeUnsigned:
		return strconv.FormatUint(v.Uint, 10), nil
	case lwm2m.TypeFloat:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return "", errf("float %v has no decimal form", v.Float)
		}
		bits := 64
		if v.Float32 {
			bits = 32
		}
		s := strconv.FormatFloat(v.Float, 'f', -1, bits)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s, nil
	case lwm2m.TypeBoolean:
		if v.Bool {
			return "1", nil
		}
		return "0", nil
	case lwm2m.TypeOpaque:
		return base64.StdEncoding.EncodeToString(v.Bytes), nil
	case lwm2m.TypeObjlnk:
		return v.Link.String(), nil
	}
	return "", errf("type %v has no plain-text form", v.Type)
}

// ExecArg is one Execute argument (Core §6.3.5): a digit 0-9, optionally
// with a value.
type ExecArg struct {
	Digit    uint8
	Value    string
	HasValue bool
}

// isChar reports whether c is in the §6.3.5 CHAR set:
// "!" / %x23-26 / %x28-5B / %x5D-7E.
func isChar(c byte) bool {
	return c == '!' || (c >= 0x23 && c <= 0x26) || (c >= 0x28 && c <= 0x5b) || (c >= 0x5d && c <= 0x7e)
}

// ParseExecArgs parses `arglist = arg *("," arg)`, `arg = DIGIT / DIGIT "="
// "'" *CHAR "'"` (Core §6.3.5). Each digit may appear once (1.2.1). An empty
// payload is no arguments. Tolerance (ambiguity A-23): SP is accepted inside
// a value, because the spec's own example has one.
func ParseExecArgs(s string) ([]ExecArg, error) {
	if s == "" {
		return nil, nil
	}
	var out []ExecArg
	var seen [10]bool
	for i := 0; ; {
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return nil, errf("execute args %q: digit expected at %d", s, i)
		}
		a := ExecArg{Digit: s[i] - '0'}
		if seen[a.Digit] {
			return nil, errf("execute args %q: digit %d repeated", s, a.Digit)
		}
		seen[a.Digit] = true
		i++
		if i < len(s) && s[i] == '=' {
			if i+1 >= len(s) || s[i+1] != '\'' {
				return nil, errf("execute args %q: quote expected at %d", s, i+1)
			}
			j := i + 2
			for j < len(s) && (isChar(s[j]) || s[j] == ' ') {
				j++
			}
			if j >= len(s) || s[j] != '\'' {
				return nil, errf("execute args %q: unterminated or invalid value", s)
			}
			a.Value, a.HasValue = s[i+2:j], true
			i = j + 1
		}
		out = append(out, a)
		if i == len(s) {
			return out, nil
		}
		if s[i] != ',' {
			return nil, errf("execute args %q: ',' expected at %d", s, i)
		}
		i++
	}
}

// FormatExecArgs renders arguments strictly per §6.3.5: digits 0-9, each at
// most once, values only from CHAR (no SP, quote or backslash).
func FormatExecArgs(args []ExecArg) (string, error) {
	var b strings.Builder
	var seen [10]bool
	for i, a := range args {
		if a.Digit > 9 || seen[a.Digit] {
			return "", errf("execute arg digit %d invalid or repeated", a.Digit)
		}
		seen[a.Digit] = true
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('0' + a.Digit)
		if a.HasValue {
			for k := 0; k < len(a.Value); k++ {
				if !isChar(a.Value[k]) {
					return "", errf("execute arg %d: byte %q not allowed", a.Digit, a.Value[k])
				}
			}
			b.WriteString("='" + a.Value + "'")
		}
	}
	return b.String(), nil
}
