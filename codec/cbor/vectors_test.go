package cbor

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// nonCanonical lists encode vectors whose source (Zephyr) uses a form we do
// not emit; for them the test proves decode(payload) == expected and
// decode(Encode(expected)) == expected.
var nonCanonical = map[string]string{
	"cbor-zephyr-put-time-1":   "Zephyr writes tag 0 + RFC 3339; we write tag 1 + epoch (RFC 8949 §3.4.2)",
	"cbor-zephyr-put-objlnk-1": "Zephyr includes a trailing NUL in the text (C7)",
	"cbor-zephyr-put-objlnk-2": "Zephyr includes a trailing NUL in the text (C7)",
	"cbor-zephyr-put-objlnk-3": "Zephyr includes a trailing NUL in the text (C7)",
}

// placeholder is the request path for the vectors: Zephyr's CBOR tests use
// no path, and single-value formats ignore the node path (vectors README).
var placeholder = lwm2m.MustParsePath("/0/0/0")

// TestVectors runs every cbor.json vector (Core §7.5.3, Table C-2).
//
// Proves: CBOR-12
func TestVectors(t *testing.T) {
	vs, err := vectors.Load("cbor")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	pass := 0
	for _, v := range vs {
		seen[v.ID] = true
		if t.Run(v.ID, func(t *testing.T) { checkVector(t, v) }) {
			pass++
		}
	}
	t.Logf("cbor: %d/%d vectors pass", pass, len(vs))
	for id := range nonCanonical {
		if !seen[id] {
			t.Errorf("nonCanonical lists unknown vector %s", id)
		}
	}
}

func checkVector(t *testing.T, v vectors.Vector) {
	payload, err := v.Payload()
	if err != nil {
		t.Fatal(err)
	}
	var c Codec
	if v.IsError() {
		if ns, err := c.Decode(placeholder, payload, nil); err == nil {
			t.Errorf("accepted: %v", ns)
		}
		return
	}
	want, err := v.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		want[i].Path = placeholder
	}
	s := vectors.Schema(want)
	decodeEq := func(what string, b []byte) {
		got, err := c.Decode(placeholder, b, s)
		if err != nil || !lwm2m.NodesEqual(got, want) {
			t.Errorf("%s %x: got %v (%v) want %v", what, b, got, err, want)
		}
	}
	if v.Decodes() {
		decodeEq("payload", payload)
	}
	if v.Encodes() {
		got, err := c.Encode(placeholder, want)
		if err != nil {
			t.Fatal(err)
		}
		reason := nonCanonical[v.ID]
		switch {
		case bytes.Equal(got, payload):
			if reason != "" {
				t.Errorf("listed as nonCanonical (%s) but byte-exact", reason)
			}
		case reason != "":
			decodeEq("payload", payload)
			decodeEq("our encoding", got)
		default:
			t.Errorf("Encode got %x want %x", got, payload)
		}
	}
}
