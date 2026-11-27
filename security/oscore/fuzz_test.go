package oscore

import (
	"reflect"
	"testing"
)

func FuzzParseHeader(f *testing.F) {
	// Option values from the RFC 8613 C.4-C.6 request vectors.
	for _, s := range []string{"", "0914", "091400", "19140837cbf3210017a2d3", "20", "06010203040506", "0301", "11010500", "010109", "08", "1000"} {
		f.Add(h(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		got, err := ParseHeader(input)
		if err != nil {
			return
		}
		again, err := ParseHeader(got.encode())
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatalf("header round trip: %+v => %+v, %v", got, again, err)
		}
	})
}
