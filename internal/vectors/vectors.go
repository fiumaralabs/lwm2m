// Package vectors loads the golden test vectors in spec/vectors and converts
// their value model into lwm2m.Node lists. See spec/vectors/README.md.
package vectors

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/fiumaralabs/lwm2m"
)

// Vector is one entry of a spec/vectors/*.json file.
type Vector struct {
	ID            string          `json:"id"`
	Source        string          `json:"source"`
	Direction     string          `json:"direction"` // encode | decode | both
	Path          string          `json:"path"`
	ContentFormat *int            `json:"content_format"`
	BytesHex      *string         `json:"bytes_hex"`
	Text          *string         `json:"text"`
	Expected      json.RawMessage `json:"expected"`
	Notes         string          `json:"notes"`
	Depth         *int            `json:"depth"`
}

// Dir returns the absolute path of spec/vectors.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "spec", "vectors")
}

// Load reads spec/vectors/<name>.json.
func Load(name string) ([]Vector, error) {
	b, err := os.ReadFile(filepath.Join(Dir(), name+".json"))
	if err != nil {
		return nil, err
	}
	var vs []Vector
	if err := json.Unmarshal(b, &vs); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return vs, nil
}

// Payload returns the vector's payload: bytes_hex decoded, or text.
func (v Vector) Payload() ([]byte, error) {
	if v.BytesHex != nil {
		return hex.DecodeString(*v.BytesHex)
	}
	if v.Text != nil {
		return []byte(*v.Text), nil
	}
	return nil, nil
}

func (v Vector) HasPayload() bool { return v.BytesHex != nil || v.Text != nil }

// Decodes reports whether the payload must decode to Expected.
func (v Vector) Decodes() bool { return v.Direction == "decode" || v.Direction == "both" }

// Encodes reports whether Expected must encode to the payload.
func (v Vector) Encodes() bool { return v.Direction == "encode" || v.Direction == "both" }

// IsError reports whether Expected is the string "error".
func (v Vector) IsError() bool { return string(v.Expected) == `"error"` }

// ExpectedShape returns "nodes" for a node list, "error", or the single key
// of a format-specific object such as "links", "paths", "senml", "tlv".
func (v Vector) ExpectedShape() string {
	if v.IsError() {
		return "error"
	}
	if len(v.Expected) > 0 && v.Expected[0] == '[' {
		return "nodes"
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(v.Expected, &m) == nil {
		for k := range m {
			return k
		}
	}
	return "unknown"
}

// ExpectedObject unmarshals the value under key of a format-specific object.
func (v Vector) ExpectedObject(key string, out any) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v.Expected, &m); err != nil {
		return err
	}
	raw, ok := m[key]
	if !ok {
		return fmt.Errorf("expected has no %q", key)
	}
	return json.Unmarshal(raw, out)
}

// BasePath parses v.Path; an empty or missing path is the root.
func (v Vector) BasePath() (lwm2m.Path, error) {
	if v.Path == "" {
		return lwm2m.Root, nil
	}
	return lwm2m.ParsePath(v.Path)
}

// JSONNode is the vector value model for one node.
type JSONNode struct {
	Path   string          `json:"path"`
	Type   string          `json:"type"`
	Value  json.RawMessage `json:"value"`
	Time   *float64        `json:"time"`
	Prefix string          `json:"prefix"`
}

// Nodes converts Expected (a node list) into lwm2m nodes. Nodes whose path
// is a placeholder ("") get the root path.
func (v Vector) Nodes() ([]lwm2m.Node, error) {
	var js []JSONNode
	if err := json.Unmarshal(v.Expected, &js); err != nil {
		return nil, err
	}
	out := make([]lwm2m.Node, 0, len(js))
	for _, j := range js {
		n, err := j.Node()
		if err != nil {
			return nil, fmt.Errorf("%s: node %s: %w", v.ID, j.Path, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// Node converts one JSON node.
func (j JSONNode) Node() (lwm2m.Node, error) {
	var n lwm2m.Node
	if j.Path != "" {
		p, err := lwm2m.ParsePath(j.Path)
		if err != nil {
			return n, err
		}
		n.Path = p
	}
	if j.Time != nil {
		n.Time, n.HasTime = *j.Time, true
	}
	switch j.Type {
	case "instance":
		n.Kind = lwm2m.KindEmptyInstance
		return n, nil
	case "multiple":
		n.Kind = lwm2m.KindEmptyMultiple
		return n, nil
	}
	t, err := lwm2m.ParseType(j.Type)
	if err != nil {
		return n, err
	}
	val, err := decodeValue(t, j.Value)
	if err != nil {
		return n, err
	}
	n.Value = val
	return n, nil
}

func decodeValue(t lwm2m.Type, raw json.RawMessage) (lwm2m.Value, error) {
	var val lwm2m.Value
	val.Type = t
	switch t {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return val, json.Unmarshal(raw, &val.Str)
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		i, err := numOrString(raw, 64, true)
		val.Int = int64(i)
		return val, err
	case lwm2m.TypeUnsigned:
		u, err := numOrString(raw, 64, false)
		val.Uint = u
		return val, err
	case lwm2m.TypeFloat:
		var s string
		if json.Unmarshal(raw, &s) == nil {
			switch s {
			case "NaN":
				val.Float = math.NaN()
			case "Infinity":
				val.Float = math.Inf(1)
			case "-Infinity":
				val.Float = math.Inf(-1)
			default:
				f, err := strconv.ParseFloat(s, 64)
				val.Float = f
				return val, err
			}
			return val, nil
		}
		return val, json.Unmarshal(raw, &val.Float)
	case lwm2m.TypeBoolean:
		return val, json.Unmarshal(raw, &val.Bool)
	case lwm2m.TypeOpaque:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return val, err
		}
		b, err := hex.DecodeString(s)
		val.Bytes = b
		if val.Bytes == nil {
			val.Bytes = []byte{}
		}
		return val, err
	case lwm2m.TypeObjlnk:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return val, err
		}
		l, err := lwm2m.ParseObjLink(s)
		val.Link = l
		return val, err
	case lwm2m.TypeNone:
		return val, nil
	}
	return val, fmt.Errorf("unsupported type %v", t)
}

// numOrString reads a JSON number or a decimal string as a 64-bit value.
func numOrString(raw json.RawMessage, bits int, signed bool) (uint64, error) {
	s := string(raw)
	var str string
	if json.Unmarshal(raw, &str) == nil {
		s = str
	}
	if signed {
		i, err := strconv.ParseInt(s, 10, bits)
		if err != nil {
			f, ferr := strconv.ParseFloat(s, 64)
			if ferr != nil || f != math.Trunc(f) {
				return 0, err
			}
			return uint64(int64(f)), nil
		}
		return uint64(i), nil
	}
	return strconv.ParseUint(s, 10, bits)
}

// Schema derives resource definitions from the expected nodes: the type of
// each resource, and Multiple when a resource-instance path appears. Used to
// decode untyped formats in tests.
func Schema(nodes []lwm2m.Node) lwm2m.Schema {
	defs := map[lwm2m.Path]lwm2m.ResourceDef{}
	for _, n := range nodes {
		switch {
		case n.Kind == lwm2m.KindEmptyMultiple:
			d := defs[n.Path]
			d.Multiple = true
			defs[n.Path] = d
		case n.Kind == lwm2m.KindValue && n.Path.IsResourceInstance():
			r := n.Path.Parent()
			defs[r] = lwm2m.ResourceDef{Type: n.Value.Type, Multiple: true}
		case n.Kind == lwm2m.KindValue:
			defs[n.Path] = lwm2m.ResourceDef{Type: n.Value.Type}
		}
	}
	return lwm2m.SchemaFunc(func(p lwm2m.Path) (lwm2m.ResourceDef, bool) {
		d, ok := defs[p]
		return d, ok
	})
}
