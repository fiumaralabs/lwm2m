package lwm2mcbor

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists encode/both vectors whose source encoder picks another
// legal layout than ours. None so far: our own vectors use our layout and
// the TS examples are decode-only (see TestEncodeMatchesSpecExamples).
var nonCanonical = map[string]string{}

// noEncode lists vectors that cannot go through Encode at all.
var noEncode = map[string]string{
	"lwcbor-own-empty-top-definite": "no nodes: the encoder refuses an empty map (grammar 1*(ID, VALUE))",
	"lwcbor-own-empty-top-indef":    "no nodes: the encoder refuses an empty map (grammar 1*(ID, VALUE))",
}

func loadVectors(t *testing.T) []vectors.Vector {
	t.Helper()
	var out []vectors.Vector
	for _, f := range []string{"lwm2m_cbor", "spec-examples"} {
		vs, err := vectors.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if v.ContentFormat != nil && *v.ContentFormat == int(lwm2m.FormatLwM2MCBOR) {
				out = append(out, v)
			}
		}
	}
	return out
}

// testPath parses a path allowing ID 65535 (Create with no instance
// reference) and a leading non-numeric Gateway prefix segment ("/d01/3/0/0").
func testPath(t *testing.T, s string) (string, lwm2m.Path) {
	t.Helper()
	segs := strings.Split(strings.Trim(s, "/"), "/")
	prefix := ""
	if len(segs) > 0 && segs[0] != "" && strings.Trim(segs[0], "0123456789") != "" {
		prefix, segs = segs[0], segs[1:]
	}
	var ids []uint16
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		id, err := strconv.ParseUint(seg, 10, 16)
		if err != nil {
			t.Fatalf("path %q: %v", s, err)
		}
		ids = append(ids, uint16(id))
	}
	return prefix, lwm2m.NewPath(ids...)
}

// expected returns the vector's nodes, Gateway prefixes in Node.Prefix.
func expected(t *testing.T, v vectors.Vector) []lwm2m.Node {
	t.Helper()
	var js []vectors.JSONNode
	if err := json.Unmarshal(v.Expected, &js); err != nil {
		t.Fatal(err)
	}
	out := []lwm2m.Node{}
	for _, j := range js {
		prefix, p := testPath(t, j.Path)
		if j.Prefix != "" {
			prefix = j.Prefix
		}
		j.Path = ""
		n, err := j.Node()
		if err != nil {
			t.Fatal(err)
		}
		n.Prefix, n.Path = prefix, p
		out = append(out, n)
	}
	return out
}

// Proves: CBOR-01, CBOR-02, CBOR-03, CBOR-04, CBOR-05, CBOR-06, CBOR-07, CBOR-08
func TestVectors(t *testing.T) {
	c := Codec{}
	used := map[string]bool{}
	n := 0
	for _, v := range loadVectors(t) {
		n++
		t.Run(v.ID, func(t *testing.T) {
			_, base := testPath(t, v.Path)
			payload, err := v.Payload()
			if err != nil || !v.HasPayload() {
				t.Fatalf("no payload: %v", err)
			}
			switch v.ExpectedShape() {
			case "error":
				if !v.Decodes() {
					t.Fatal("error vector must be decode")
				}
				got, err := c.Decode(base, payload, nil)
				if err == nil {
					t.Fatalf("decode succeeded, want error: %v", got)
				}
				t.Logf("rejected: %v", err)
				return
			case "nodes":
			default:
				t.Fatalf("unhandled expected shape %q", v.ExpectedShape())
			}
			want := expected(t, v)
			s := vectors.Schema(want)
			if v.Decodes() {
				got, err := c.Decode(base, payload, s)
				if err != nil {
					t.Fatal(err)
				}
				if !lwm2m.NodesEqual(got, want) {
					t.Fatalf("decode:\n got %v\nwant %v", got, want)
				}
			}
			if reason, ok := noEncode[v.ID]; ok {
				used[v.ID] = true
				if enc, err := c.Encode(base, want); err == nil {
					t.Fatalf("listed in noEncode (%s) but encodes to %x", reason, enc)
				}
				return
			}
			enc, err := c.Encode(base, want)
			if err != nil {
				t.Fatal(err)
			}
			exact := bytes.Equal(enc, payload)
			reason, listed := nonCanonical[v.ID]
			used[v.ID] = used[v.ID] || listed
			switch {
			case v.Encodes() && exact && listed:
				t.Fatalf("listed as non-canonical (%s) but encodes byte-exact", reason)
			case v.Encodes() && !exact && !listed:
				t.Fatalf("encode:\n got %x\nwant %x", enc, payload)
			}
			if !exact {
				got, err := c.Decode(base, enc, s)
				if err != nil || !lwm2m.NodesEqual(got, want) {
					t.Fatalf("round trip of %x: %v\n%s", enc, err, lwm2m.FormatNodes(got))
				}
			}
		})
	}
	for id := range nonCanonical {
		if !used[id] {
			t.Errorf("nonCanonical lists %s, which is not a non-canonical encode vector", id)
		}
	}
	for id := range noEncode {
		if !used[id] {
			t.Errorf("noEncode lists unknown vector %s", id)
		}
	}
	t.Logf("%d LwM2M CBOR vectors", n)
}
