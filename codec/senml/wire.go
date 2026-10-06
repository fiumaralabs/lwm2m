package senml

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	fx "github.com/fxamacker/cbor/v2"

	"github.com/fiumaralabs/lwm2m"
	cborc "github.com/fiumaralabs/lwm2m/codec/cbor"
)

// RFC 8428 §6 Table 6 integer labels.
var labels = map[int64]string{
	-1: "bver", -2: "bn", -3: "bt", -4: "bu", -5: "bv", -6: "bs",
	0: "n", 1: "u", 2: "v", 3: "vs", 4: "vb", 5: "s", 6: "t", 7: "ut", 8: "vd",
}

var cborKeys = map[string]any{"bn": -2, "bt": -3, "n": 0, "v": 2, "vs": 3, "vb": 4, "t": 6, "vd": 8, "vlo": "vlo"}

func decodeJSON(data []byte) ([]Record, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var ms []map[string]any
	// ponytail: bytes after the array are ignored (Leshan accepts a literal with a trailing comma)
	if err := dec.Decode(&ms); err != nil {
		return nil, fmt.Errorf("%w: %v", errSenML, err)
	}
	return buildAll(ms, false)
}

func decodeCBOR(data []byte) ([]Record, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var raw []map[any]any
	if err := fx.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", errSenML, err)
	}
	ms := make([]map[string]any, len(raw))
	for i, m := range raw {
		ms[i] = map[string]any{}
		for k, x := range m {
			var name string
			switch k := k.(type) {
			case string: // "vlo" (Core §7.5.7), or a textual label
				name = k
			case uint64:
				name = labels[int64(k)]
			case int64:
				name = labels[k]
			}
			if name == "" {
				name = fmt.Sprint("label ", k)
			}
			ms[i][name] = x
		}
	}
	return buildAll(ms, true)
}

func buildAll(ms []map[string]any, isCBOR bool) ([]Record, error) {
	rs := make([]Record, len(ms))
	for i, m := range ms {
		var err error
		if rs[i], err = build(m, isCBOR); err != nil {
			return nil, err
		}
	}
	return rs, nil
}

// jsonNumber parses a JSON number exactly: integer literals stay integers
// (no float64 round trip, A-24), anything with a fraction or exponent is a
// float.
func jsonNumber(s string) (lwm2m.Value, error) {
	if strings.ContainsAny(s, ".eE") {
		f, err := strconv.ParseFloat(s, 64)
		return lwm2m.Float(f), err
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return lwm2m.Integer(i), nil
	}
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return lwm2m.Value{}, fmt.Errorf("%w: number %s out of range", errSenML, s)
	}
	return lwm2m.Unsigned(u), nil
}

// decodeBase64 accepts base64url without padding (Table C-2) and, as a
// tolerance, the standard alphabet and padding (Table 7.5.6-1, A-24).
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: vd: %v", errSenML, err)
	}
	return b, nil
}

// timeNum is a bt or t value.
type timeNum float64

// fields lists a record's fields in emission order: bn, bt, n, value, t.
func fields(r Record) (keys []string, vals []any) {
	add := func(k string, v any) { keys, vals = append(keys, k), append(vals, v) }
	if r.BaseName != "" {
		add("bn", r.BaseName)
	}
	if r.BaseTime != 0 {
		add("bt", timeNum(r.BaseTime))
	}
	if r.Name != "" {
		add("n", r.Name)
	}
	if r.Field != "" {
		add(r.Field, r.Value)
	}
	if r.Time != 0 {
		add("t", timeNum(r.Time))
	}
	return keys, vals
}

func encodeJSON(rs []Record) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(',')
		}
		keys, vals := fields(r)
		b.WriteByte('{')
		for j, k := range keys {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%q:", k)
			if err := jsonValue(&b, vals[j]); err != nil {
				return nil, err
			}
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

func jsonValue(b *bytes.Buffer, x any) error {
	switch x := x.(type) {
	case string:
		return jsonString(b, x)
	case timeNum:
		b.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 64))
		return nil
	}
	v := x.(lwm2m.Value)
	switch v.Type {
	case lwm2m.TypeNone:
		b.WriteString("null")
	case lwm2m.TypeInteger:
		b.WriteString(strconv.FormatInt(v.Int, 10))
	case lwm2m.TypeUnsigned:
		b.WriteString(strconv.FormatUint(v.Uint, 10))
	case lwm2m.TypeFloat:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return fmt.Errorf("%w: %v has no JSON form", errSenML, v.Float)
		}
		s := strconv.FormatFloat(v.Float, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0" // keep a float a float for schema-less readers
		}
		b.WriteString(s)
	case lwm2m.TypeString:
		return jsonString(b, v.Str)
	case lwm2m.TypeBoolean:
		b.WriteString(strconv.FormatBool(v.Bool))
	case lwm2m.TypeOpaque:
		return jsonString(b, base64.RawURLEncoding.EncodeToString(v.Bytes))
	default:
		return fmt.Errorf("%w: cannot write %v", errSenML, v.Type)
	}
	return nil
}

func jsonString(b *bytes.Buffer, s string) error {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b.Truncate(b.Len() - 1) // Encode appends '\n'
	return nil
}

func encodeCBOR(rs []Record) ([]byte, error) {
	items := make([]fx.RawMessage, len(rs))
	for i, r := range rs {
		keys, vals := fields(r)
		m := []byte{0xa0 | byte(len(keys))} // at most 5 entries
		for j, k := range keys {
			kb, err := cborc.Marshal(cborKeys[k])
			if err != nil {
				return nil, err
			}
			vb, err := cborValue(vals[j])
			if err != nil {
				return nil, err
			}
			m = append(append(m, kb...), vb...)
		}
		items[i] = m
	}
	return cborc.Marshal(items)
}

func cborValue(x any) ([]byte, error) {
	switch x := x.(type) {
	case string:
		return cborc.Marshal(x)
	case timeNum:
		if f := float64(x); f == math.Trunc(f) && math.Abs(f) < 1<<63 {
			return cborc.Marshal(int64(f)) // integral times as integers, as Zephyr does
		}
		return cborc.Marshal(float64(x))
	}
	return cborc.EncodeValue(x.(lwm2m.Value))
}
