package link

import (
	"reflect"
	"testing"
)

func TestQuotedBackslashRoundTrip(t *testing.T) {
	for _, input := range []string{`</>;x="\0"`, `</>;x="\\"`, `</>;x="\\\""`, `</>;x="a\\b"`} {
		links, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		encoded := Encode(links)
		again, err := Parse(encoded)
		if err != nil || !reflect.DeepEqual(links, again) {
			t.Errorf("round trip %q -> %q: %#v -> %#v, %v", input, encoded, links, again, err)
		}
	}
}
