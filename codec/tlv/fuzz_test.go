package tlv_test

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/tlv"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// FuzzDecode exercises raw records, typed values and the public decoder.
// Malformed input may return an error, but must not panic or hang.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"tlv", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			f.Fatal(err)
		}
		for _, v := range vs {
			if name == "spec-examples" && (v.ContentFormat == nil || *v.ContentFormat != int(lwm2m.FormatTLV)) {
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
		_, _ = tlv.ParseRaw(b)
		for typ := lwm2m.TypeNone; typ <= lwm2m.TypeCorelnk; typ++ {
			_, _ = tlv.DecodeValue(typ, b)
			s := lwm2m.SchemaFunc(func(lwm2m.Path) (lwm2m.ResourceDef, bool) {
				return lwm2m.ResourceDef{Type: typ}, true
			})
			_, _ = (tlv.Codec{}).Decode(lwm2m.NewPath(3, 0), b, s)
		}
		_, _ = (tlv.Codec{}).Decode(lwm2m.NewPath(3), b, nil)
		_, _ = (tlv.Codec{}).Decode(lwm2m.NewPath(3, 0, 6), b, nil)
	})
}
