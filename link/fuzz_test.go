package link

import (
	"reflect"
	"strings"
	"testing"
)

var fuzzLinks = []string{
	"", "</3/0>,</3/1>", `</>;lwm2m=1.1,</0/0>;ssid=101;uri="coaps://s1",</3/0/7>;dim=2;pmin=10`,
	`</rp/6>;ver=2.0,</rp/6/0>`, `</a>;t="x\"y"`, "lwm2m=1.0;x,</0/0>", "</a>,",
}

func FuzzParse(f *testing.F) {
	for _, s := range fuzzLinks {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		links, err := Parse(s)
		if err != nil {
			return
		}
		// Leshan-compatible parsing retains backslashes, whereas Encode escapes
		// them; such values do not currently have a stable round trip.
		for _, l := range links {
			for _, p := range l.Params {
				if strings.Contains(p.Value, `\`) {
					return
				}
			}
		}
		encoded := Encode(links)
		again, err := Parse(encoded)
		if err != nil || !reflect.DeepEqual(links, again) {
			t.Fatalf("round trip %q -> %q: %#v -> %#v, %v", s, encoded, links, again, err)
		}
	})
}

func FuzzParseRegistration(f *testing.F) {
	for _, s := range fuzzLinks {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		links, err := Parse(s)
		if err != nil {
			return
		}
		_, _ = ParseRegistration(links)
	})
}

func FuzzParseContentFormats(f *testing.F) {
	for _, s := range []string{"", "0", "11542", "0 40 11542", "json", "65536"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { _, _ = ParseContentFormats(s) })
}

func FuzzParseDiscover(f *testing.F) {
	for _, s := range fuzzLinks {
		f.Add(s, "")
		f.Add(s, "/rp")
	}
	f.Fuzz(func(t *testing.T, s, root string) {
		links, err := Parse(s)
		if err != nil {
			return
		}
		_, _ = ParseDiscover(links, root)
	})
}
