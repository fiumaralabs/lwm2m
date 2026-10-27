package senml

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func FuzzDecodeJSON(f *testing.F) { fuzzDecode(f, JSON, "senml_json", "senml_etch_json") }
func FuzzDecodeCBOR(f *testing.F) { fuzzDecode(f, CBOR, "senml_cbor", "senml_etch_cbor") }

// Seed both wire formats from the golden corpus, including malformed packs.
// Errors are valid outcomes; panics and hangs are not.
func fuzzDecode(f *testing.F, c Codec, names ...string) {
	for _, name := range append(names, "spec-examples") {
		vs, err := vectors.Load(name)
		if err != nil {
			f.Fatal(err)
		}
		for _, v := range vs {
			if name == "spec-examples" && (v.ContentFormat == nil || *v.ContentFormat != int(c.Format())) {
				continue
			}
			if !v.HasPayload() {
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
		_, _ = c.DecodeRecords(b)
		_, _ = c.Decode(lwm2m.Root, b, nil)
		_, _ = c.Decode(lwm2m.NewPath(3, 0), b, nil)
		_, _ = c.WithRootPath("/d01").Decode(lwm2m.Root, b, nil)
	})
}
