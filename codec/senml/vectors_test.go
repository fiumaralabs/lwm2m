package senml

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists encode vectors whose source encoder uses a different
// (spec-legal) form than ours. For these the test proves decode(payload) ==
// expected and decode(Encode(expected)) == expected instead of byte equality.
var nonCanonical = map[string]string{
	// Leshan: bn repeated with bt on every timestamp; we send bt once + relative t.
	"senml-leshan-enc-timestamped-resource":  "Leshan repeats bn+bt per record",
	"senml-leshan-enc-timestamped-nodes":     "Leshan repeats bn+bt per record; we use full n at the root",
	"senml-cbor-leshan-encode-timestamped-1": "Leshan repeats bn and writes bt as tag-4 decimal fraction",
	// Leshan composite: bn per resource; the spec tables use full-path n.
	"senml-leshan-enc-composite-resources": "Leshan bn per resource; spec Table 7.5.6-7 uses n",
	"senml-leshan-enc-composite-mixed":     "Leshan bn per resource; spec Table 7.5.6-7 uses n",
	// Spec rendering with whitespace and full-path n on a resource write.
	"spec-senml-json-write-34-0-1": "TS rendering has whitespace and full-path n; we send bn=/34/0/1/ + n",
	// Zephyr: bn="/o/i/" + n="r" for a single-resource read (C7).
	"senml-cbor-zephyr-put-s8-1":                   "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s8-2":                   "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s8-3":                   "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s16-1":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s16-2":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s16-3":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s32-1":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s32-2":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s32-3":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s64-1":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s64-2":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-s64-3":                  "Zephyr bn/n split",
	"senml-cbor-zephyr-put-string-1":               "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-1":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-2":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-3":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-4":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-5":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-float-6":                "Zephyr bn/n split",
	"senml-cbor-zephyr-put-bool-1":                 "Zephyr bn/n split",
	"senml-cbor-zephyr-put-bool-2":                 "Zephyr bn/n split",
	"senml-cbor-zephyr-put-objlnk-1":               "Zephyr bn/n split",
	"senml-cbor-zephyr-put-objlnk-2":               "Zephyr bn/n split",
	"senml-cbor-zephyr-put-objlnk-3":               "Zephyr bn/n split",
	"senml-cbor-zephyr-put-opaque-1":               "Zephyr bn/n split",
	"senml-cbor-zephyr-put-time-1":                 "Zephyr bn/n split",
	"senml-cbor-zephyr-send-timeseries-1":          "Zephyr bn/n split; key order bn,bt,n,t,v",
	"senml-cbor-zephyr-send-timeseries-res-inst-1": "Zephyr bn at instance level; key order bn,bt,n,t,v",
}

// TestVectors runs every SenML vector (RFC 8428, RFC 8790, Core §7.5.6/7).
//
// Proves: FMT-05, FMT-07
func TestVectors(t *testing.T) {
	seen := map[string]bool{}
	files := []struct {
		name string
		keep func(vectors.Vector) bool
	}{
		{"senml_json", nil}, {"senml_cbor", nil}, {"senml_etch_json", nil}, {"senml_etch_cbor", nil},
		{"spec-examples", func(v vectors.Vector) bool {
			return v.ContentFormat != nil && (*v.ContentFormat == 110 || *v.ContentFormat == 112)
		}},
	}
	for _, f := range files {
		vs, err := vectors.Load(f.name)
		if err != nil {
			t.Fatal(err)
		}
		total, pass := 0, 0
		for _, v := range vs {
			if f.keep != nil && !f.keep(v) {
				continue
			}
			seen[v.ID] = true
			total++
			if t.Run(v.ID, func(t *testing.T) { checkVector(t, v) }) {
				pass++
			}
		}
		t.Logf("%s: %d/%d vectors pass", f.name, pass, total)
	}
	for id := range nonCanonical {
		if !seen[id] {
			t.Errorf("nonCanonical lists unknown vector %s", id)
		}
	}
}

func codecFor(t *testing.T, v vectors.Vector) Codec {
	if v.ContentFormat == nil {
		t.Fatal("no content_format")
	}
	switch lwm2m.ContentFormat(*v.ContentFormat) {
	case lwm2m.FormatSenMLJSON:
		return JSON
	case lwm2m.FormatSenMLCBOR:
		return CBOR
	case lwm2m.FormatSenMLETCHJSON:
		return ETCHJSON
	case lwm2m.FormatSenMLETCHCBOR:
		return ETCHCBOR
	}
	t.Fatalf("content_format %d", *v.ContentFormat)
	return Codec{}
}

// basePath splits the vector's request path into an alternate-path/gateway
// prefix (leading non-numeric segments, e.g. /lwm2m, /d01) and the LwM2M
// path. /dp (Send) and /bspack carry absolute names: base is the root.
func basePath(t *testing.T, v vectors.Vector) (string, lwm2m.Path) {
	if v.Path == "/dp" || v.Path == "/bspack" {
		return "", lwm2m.Root
	}
	prefix, rest := splitPrefix(v.Path)
	if rest == "" {
		return prefix, lwm2m.Root
	}
	p, err := lwm2m.ParsePath(rest)
	if err != nil {
		t.Fatal(err)
	}
	return prefix, p
}

func splitPrefix(s string) (prefix, rest string) {
	for s != "" {
		seg, _, _ := strings.Cut(strings.TrimPrefix(s, "/"), "/")
		if seg == "" || seg[0] >= '0' && seg[0] <= '9' {
			break
		}
		prefix += "/" + seg
		s = strings.TrimPrefix(strings.TrimPrefix(s, "/"), seg)
	}
	return prefix, s
}

// expectedNodes converts expected, stripping the prefix from node paths.
func expectedNodes(t *testing.T, v vectors.Vector, prefix string) []lwm2m.Node {
	var js []vectors.JSONNode
	if err := json.Unmarshal(v.Expected, &js); err != nil {
		t.Fatal(err)
	}
	out := make([]lwm2m.Node, len(js))
	for i, j := range js {
		j.Path = strings.TrimPrefix(j.Path, prefix)
		n, err := j.Node()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = n
	}
	return out
}

// expectedRecords converts {"senml":[...]} (vd is base64url in JSON vectors,
// hex in CBOR vectors) with the same field rules as the decoder.
func expectedRecords(t *testing.T, v vectors.Vector, c Codec) []Record {
	var raws []json.RawMessage
	if err := v.ExpectedObject("senml", &raws); err != nil {
		t.Fatal(err)
	}
	rs := make([]Record, len(raws))
	for i, raw := range raws {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if s, ok := m["vd"].(string); ok && c.isCBOR() {
			b, err := hex.DecodeString(s)
			if err != nil {
				t.Fatal(err)
			}
			m["vd"] = b
		}
		r, err := build(m, c.isCBOR())
		if err != nil {
			t.Fatal(err)
		}
		rs[i] = r
	}
	return rs
}

func recordsEqual(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if !x.Value.Equal(y.Value) {
			return false
		}
		x.Value, y.Value = lwm2m.Value{}, lwm2m.Value{}
		if !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}

func show(c Codec, b []byte) string {
	if c.isCBOR() {
		return hex.EncodeToString(b)
	}
	return string(b)
}

// zephyrTestObject is the Zephyr test object ID. Core §7.3 reserves 65535
// (MAX_ID), so lwm2m.ParsePath correctly refuses it; these vectors are run
// with the object renumbered to 65534 in the path, the payload names and the
// expected node paths (same digit count, so CBOR lengths are unchanged).
// Objlnk values ("65535:65535") have no slash and are left alone.
const zephyrTestObject, zephyrRemap = "/65535/", "/65534/"

func remap(v vectors.Vector) vectors.Vector {
	r := strings.NewReplacer(zephyrTestObject, zephyrRemap)
	v.Path = r.Replace(v.Path)
	v.Expected = json.RawMessage(r.Replace(string(v.Expected)))
	if v.Text != nil {
		s := r.Replace(*v.Text)
		v.Text = &s
	}
	if v.BytesHex != nil {
		h := strings.ReplaceAll(*v.BytesHex, hex.EncodeToString([]byte(zephyrTestObject)), hex.EncodeToString([]byte(zephyrRemap)))
		v.BytesHex = &h
	}
	return v
}

func checkVector(t *testing.T, v vectors.Vector) {
	v = remap(v)
	prefix, base := basePath(t, v)
	c := codecFor(t, v).WithRootPath(prefix)
	payload, err := v.Payload()
	if err != nil {
		t.Fatal(err)
	}
	reason := nonCanonical[v.ID]
	switch v.ExpectedShape() {
	case "error":
		if !v.HasPayload() {
			t.Fatal("error vector without payload")
		}
		if ns, err := c.Decode(base, payload, nil); err == nil {
			t.Errorf("Decode accepted invalid payload: %s", lwm2m.FormatNodes(ns))
		}
		if ps, err := c.DecodePaths(payload); err == nil {
			t.Errorf("DecodePaths accepted invalid payload: %v", ps)
		}
	case "nodes":
		want := expectedNodes(t, v, prefix)
		s := vectors.Schema(want)
		decodeEq := func(what string, b []byte) {
			got, err := c.Decode(base, b, s)
			if err != nil {
				t.Fatalf("%s: Decode: %v", what, err)
			}
			if !lwm2m.NodesEqual(got, want) {
				t.Errorf("%s: Decode got\n%swant\n%s", what, lwm2m.FormatNodes(got), lwm2m.FormatNodes(want))
			}
		}
		if v.Decodes() {
			decodeEq("payload", payload)
		}
		if v.Encodes() {
			got, err := c.Encode(base, want)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			switch {
			case bytes.Equal(got, payload):
				if reason != "" {
					t.Errorf("listed as nonCanonical (%s) but byte-exact", reason)
				}
			case reason != "" || strings.Contains(v.Notes, "semantic compare"):
				decodeEq("payload", payload)
				decodeEq("our encoding", got)
			default:
				t.Errorf("Encode got\n%s\nwant\n%s", show(c, got), show(c, payload))
			}
		}
	case "paths":
		var ss []string
		if err := v.ExpectedObject("paths", &ss); err != nil {
			t.Fatal(err)
		}
		want := make([]lwm2m.Path, len(ss))
		for i, s := range ss {
			want[i] = lwm2m.MustParsePath(s)
		}
		if v.Decodes() {
			got, err := c.DecodePaths(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("DecodePaths got %v want %v", got, want)
			}
		}
		if v.Encodes() {
			got, err := c.EncodePaths(want)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("EncodePaths got %s (%v) want %s", show(c, got), err, show(c, payload))
			}
		}
	case "senml":
		want := expectedRecords(t, v, c)
		if v.Decodes() {
			got, err := c.DecodeRecords(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !recordsEqual(got, want) {
				t.Errorf("DecodeRecords got %+v want %+v", got, want)
			}
		}
		if v.Encodes() {
			got, err := c.EncodeRecords(want)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("EncodeRecords got %s (%v) want %s", show(c, got), err, show(c, payload))
			}
		}
	default:
		t.Fatalf("unsupported expected shape %q", v.ExpectedShape())
	}
}
