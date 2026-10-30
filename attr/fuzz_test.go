package attr

import (
	"reflect"
	"testing"
)

func FuzzParseQuery(f *testing.F) {
	for _, s := range []string{"", "pmin=10&pmax&gt=2.5", "pmin", "gt=-30.5", "lt=1e3", "edge=2", "ssid=65535", "ver=2.10", "uri=coaps://x:5684"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := ParseQuery(s)
		if err != nil {
			return
		}
		q := a.Query()
		b, err := ParseQuery(q)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("round trip %q -> %q: %#v -> %#v, %v", s, q, a, b, err)
		}
	})
}

func FuzzParseLinkParam(f *testing.F) {
	for _, seed := range []struct {
		name, value string
		has         bool
	}{
		{"pmin", "10", true}, {"edge", "2", true}, {"ssid", "0", true}, {"ver", "1.1", true}, {"pmin", "", false}, {"unknown", "x", true},
	} {
		f.Add(seed.name, seed.value, seed.has)
	}
	f.Fuzz(func(t *testing.T, name, value string, has bool) {
		a, known, err := ParseLinkParam(name, value, has)
		if err != nil || !known {
			return
		}
		b, known, err := ParseLinkParam(name, FormatValue(a.Value), true)
		if err != nil || !known || !reflect.DeepEqual(a, b) {
			t.Fatalf("round trip %#v -> %#v, known=%v, %v", a, b, known, err)
		}
	})
}

func FuzzParseValue(f *testing.F) {
	for _, seed := range []struct{ name, value string }{
		{"pmin", "0"}, {"gt", "-30.5"}, {"lt", "1e3"}, {"st", "0.5"}, {"edge", "1"}, {"dim", "65535"}, {"ssid", "0"}, {"ver", "2.10"}, {"uri", "coaps://x:5684"}, {"unknown", "1"},
	} {
		f.Add(seed.name, seed.value)
	}
	f.Fuzz(func(t *testing.T, name, value string) {
		v, err := ParseValue(name, value)
		if err != nil {
			return
		}
		again, err := ParseValue(name, FormatValue(v))
		if err != nil || !reflect.DeepEqual(v, again) {
			t.Fatalf("round trip %q=%q: %#v -> %#v, %v", name, value, v, again, err)
		}
	})
}
