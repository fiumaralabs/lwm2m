package lwm2m_test

import (
	"testing"

	lwm2m "github.com/fiumaralabs/lwm2m"
)

func FuzzParseObjLink(f *testing.F) {
	for _, s := range []string{"", "0:0", "65535:65535", "65536:0", "00001:2", ":", "1:2:3", " 1:2", "+1:2", "-1:2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		link, err := lwm2m.ParseObjLink(s)
		if err != nil {
			return
		}
		formatted := link.String()
		again, err := lwm2m.ParseObjLink(formatted)
		if err != nil || again != link || again.String() != formatted {
			t.Fatalf("round-trip %q -> %q: got %v, error %v", s, formatted, again, err)
		}
	})
}

func FuzzParseType(f *testing.F) {
	for _, s := range []string{"", "none", "string", "integer", "unsigned", "unsigned integer", "unsignedinteger", "float", "boolean", "opaque", "time", "objlnk", "corelnk", " STRING ", "\tUnsigned Integer\n", "65535", "+string", "string:string"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		value, err := lwm2m.ParseType(s)
		if err != nil {
			return
		}
		formatted := value.String()
		again, err := lwm2m.ParseType(formatted)
		if err != nil || again != value || again.String() != formatted {
			t.Fatalf("round-trip %q -> %q: got %v, error %v", s, formatted, again, err)
		}
	})
}
