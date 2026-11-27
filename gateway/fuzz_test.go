package gateway

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

func FuzzParsePath(f *testing.F) {
	for _, s := range []string{"/3/0", "/d01/3303/0", "/d01", "", "/", "/65536/0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePath(s)
		if err != nil {
			return
		}
		q, err := ParsePath(p.String())
		if err != nil || p != q {
			t.Fatalf("path %q reparse = %v, %v; want %v", s, q, err, p)
		}
	})
}

func FuzzDecode(f *testing.F) {
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR} {
		b, err := Encode(cf, Path{}, composite)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint16(cf), b)
	}
	f.Add(uint16(lwm2m.FormatSenMLJSON), []byte(`[{"bn":"/d01/3303/0/","n":"5700","v":1}]`))
	f.Add(uint16(lwm2m.FormatLwM2MCBOR), []byte{})
	f.Fuzz(func(t *testing.T, format uint16, data []byte) {
		cf := lwm2m.ContentFormat(format)
		nodes, err := Decode(cf, Path{}, data, nil)
		if err != nil {
			return
		}
		b, err := Encode(cf, Path{}, nodes)
		if err != nil {
			return
		}
		_, err = Decode(cf, Path{}, b, nil)
		if err != nil {
			t.Fatalf("encoded nodes cannot be decoded: %v", err)
		}
	})
}

func FuzzParseDevices(f *testing.F) {
	f.Add("2.0", "urn:dev:os:32473-101", "d01", "</3/0>,</3303/0>")
	f.Add("1.0", "id", "d02", "")
	f.Add("2.0", "", "42", "<bad>")
	f.Fuzz(func(t *testing.T, version, id, prefix, objects string) {
		nodes := []lwm2m.Node{
			lwm2m.ValueNode(lwm2m.NewPath(25, 0, 0), lwm2m.String(id)),
			lwm2m.ValueNode(lwm2m.NewPath(25, 0, 1), lwm2m.String(prefix)),
			lwm2m.ValueNode(lwm2m.NewPath(25, 0, 3), lwm2m.Corelnk(objects)),
		}
		reg, err := ParseDevices(version, nodes)
		if err != nil {
			return
		}
		if len(reg.Devices) != 1 || reg.Devices[0].ID != id || reg.Devices[0].Prefix != prefix {
			t.Fatalf("unexpected registry: %+v", reg)
		}
		if err := ValidPrefix(reg.Devices[0].Prefix); err != nil {
			t.Fatalf("invalid parsed prefix: %v", err)
		}
	})
}
