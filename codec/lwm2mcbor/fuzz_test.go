package lwm2mcbor

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// FuzzDecode covers definite/indefinite maps, array keys and gateway prefixes
// from the golden vectors. Invalid payloads may fail, but must terminate safely.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"lwm2m_cbor", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			f.Fatal(err)
		}
		for _, v := range vs {
			if v.ContentFormat == nil || *v.ContentFormat != int(lwm2m.FormatLwM2MCBOR) || !v.HasPayload() {
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
		_, _ = (Codec{}).Decode(lwm2m.Root, b, nil)
		_, _ = (Codec{}).Decode(lwm2m.NewPath(3, 0), b, nil)
	})
}
