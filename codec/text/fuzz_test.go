package text_test

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/text"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

// FuzzDecode tries each resource type because plain text has no wire type.
// Execute argument vectors also exercise their independent parser.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"plain_text", "objlnk", "spec-examples"} {
		vs, err := vectors.Load(name)
		if err != nil {
			f.Fatal(err)
		}
		for _, v := range vs {
			if name == "spec-examples" && (v.ContentFormat == nil || *v.ContentFormat != int(lwm2m.FormatText)) {
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
		for typ := lwm2m.TypeNone; typ <= lwm2m.TypeCorelnk; typ++ {
			_, _ = (text.Codec{}).Decode(lwm2m.NewPath(3, 0, 0), b, singleSchema(typ))
		}
		_, _ = (text.Codec{}).Decode(lwm2m.NewPath(3, 0, 6, 0), b, nil)
		_, _ = text.ParseExecArgs(string(b))
	})
}
