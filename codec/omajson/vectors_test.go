package omajson

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists encode vectors whose source encoder legitimately picks
// another layout than ours. For them decode(payload) and
// decode(Encode(expected)) must both equal expected.
var nonCanonical = map[string]string{
	"json-leshan-enc-timestamped-resource":          `Leshan puts the full resource path in bn and omits n; we write bn="/o/i/" + n="r" (Zephyr form), equally valid`,
	"json-leshan-enc-timestamped-resource-instance": `Leshan puts the full resource-instance path in bn and omits n; we write bn="/o/i/" + n="r/ri"`,
}

func loadVectors(t *testing.T) []vectors.Vector {
	t.Helper()
	var out []vectors.Vector
	for _, f := range []string{"oma_json", "spec-examples"} {
		vs, err := vectors.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if v.ContentFormat != nil && lwm2m.ContentFormat(*v.ContentFormat).Canonical() == lwm2m.FormatOMAJSON {
				out = append(out, v)
			}
		}
	}
	return out
}

// expectedNodes is v.Nodes() with paths parsed by parsePath, because the
// Zephyr vectors use object 65535, which lwm2m.ParsePath rejects.
func expectedNodes(t *testing.T, v vectors.Vector) []lwm2m.Node {
	t.Helper()
	var js []vectors.JSONNode
	if err := json.Unmarshal(v.Expected, &js); err != nil {
		t.Fatal(err)
	}
	out := make([]lwm2m.Node, 0, len(js))
	for _, j := range js {
		p, err := parsePath(j.Path)
		if err != nil {
			t.Fatal(err)
		}
		j.Path = ""
		n, err := j.Node()
		if err != nil {
			t.Fatal(err)
		}
		n.Path = p
		out = append(out, n)
	}
	return out
}

// equal is lwm2m.NodesEqual with timestamped values of one path compared in
// time order (NodesEqual keeps input order for equal paths).
func equal(a, b []lwm2m.Node) bool {
	byTime := func(ns []lwm2m.Node) []lwm2m.Node {
		ns = append([]lwm2m.Node(nil), ns...)
		sort.SliceStable(ns, func(i, j int) bool { return ns[i].Time < ns[j].Time })
		return ns
	}
	return lwm2m.NodesEqual(byTime(a), byTime(b))
}

// Proves: FMT-10
func TestVectors(t *testing.T) {
	c := Codec{}
	used := map[string]bool{}
	n := 0
	for _, v := range loadVectors(t) {
		n++
		t.Run(v.ID, func(t *testing.T) {
			base, err := parsePath(v.Path)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := v.Payload()
			switch v.ExpectedShape() {
			case "error":
				if !v.HasPayload() || !v.Decodes() {
					t.Fatal("error vector without a decode payload")
				}
				if got, err := c.Decode(base, payload, nil); err == nil {
					t.Fatalf("decode succeeded, want error: %v", got)
				}
			case "json":
				var want Document
				if err := v.ExpectedObject("json", &want); err != nil {
					t.Fatal(err)
				}
				got, err := DecodeDocument(payload)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(*got, want) {
					t.Fatalf("document mismatch:\n got %+v\nwant %+v", *got, want)
				}
				enc, err := EncodeDocument(&want)
				if err != nil {
					t.Fatal(err)
				}
				if v.Encodes() && !bytes.Equal(enc, payload) {
					t.Fatalf("encode:\n got %s\nwant %s", enc, payload)
				}
			case "nodes":
				want := expectedNodes(t, v)
				s := vectors.Schema(want)
				if v.Decodes() {
					got, err := c.Decode(base, payload, s)
					if err != nil {
						t.Fatal(err)
					}
					if !equal(got, want) {
						t.Fatalf("decode:\n got %s\nwant %s", lwm2m.FormatNodes(got), lwm2m.FormatNodes(want))
					}
				}
				enc, err := c.Encode(base, want)
				if err != nil {
					t.Fatal(err)
				}
				exact := bytes.Equal(enc, payload)
				reason, listed := nonCanonical[v.ID]
				used[v.ID] = listed
				switch {
				case v.Encodes() && exact && listed:
					t.Fatalf("listed as non-canonical (%s) but encodes byte-exact", reason)
				case v.Encodes() && !exact && !listed:
					t.Fatalf("encode:\n got %s\nwant %s", enc, payload)
				}
				if !exact {
					if listed {
						got, err := c.Decode(base, payload, s)
						if err != nil || !equal(got, want) {
							t.Fatalf("non-canonical payload does not decode to expected: %v\n%s", err, lwm2m.FormatNodes(got))
						}
					}
					got, err := c.Decode(base, enc, s)
					if err != nil || !equal(got, want) {
						t.Fatalf("round trip of %s: %v\n%s", enc, err, lwm2m.FormatNodes(got))
					}
				}
			default:
				t.Fatalf("unhandled expected shape %q", v.ExpectedShape())
			}
		})
	}
	for id := range nonCanonical {
		if !used[id] {
			t.Errorf("nonCanonical lists %s, which is not a non-canonical encode vector", id)
		}
	}
	t.Logf("%d OMA JSON vectors", n)
}
