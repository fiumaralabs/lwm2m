package omajson

import (
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func FuzzDecodeDocument(f *testing.F) {
	for _, name := range []string{"oma_json", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			f.Fatal(err)
		}
		for _, v := range vs {
			if !v.HasPayload() || v.ContentFormat == nil || lwm2m.ContentFormat(*v.ContentFormat).Canonical() != lwm2m.FormatOMAJSON {
				continue
			}
			b, err := v.Payload()
			if err != nil {
				f.Fatal(err)
			}
			f.Add(b)
		}
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		doc, err := DecodeDocument(b)
		if err != nil {
			return
		}
		encoded, err := EncodeDocument(doc)
		if err != nil {
			t.Fatalf("encode decoded document: %v", err)
		}
		again, err := DecodeDocument(encoded)
		if err != nil || !reflect.DeepEqual(doc, again) {
			t.Fatalf("document round trip: got %#v, want %#v, error %v", again, doc, err)
		}
	})
}
