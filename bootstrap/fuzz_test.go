package bootstrap

import (
	"bytes"
	"testing"
)

func FuzzSecurityMode(f *testing.F) {
	for _, s := range []string{`"PSK"`, `"NO_SEC"`, `"EST"`, `0`, `4`, `255`, `null`, `"unknown"`, `{}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var m SecurityMode
		if err := m.UnmarshalJSON(data); err != nil {
			return
		}
		encoded, err := m.MarshalJSON()
		if err != nil {
			return
		} // Unknown numeric modes are accepted but cannot be marshaled.
		var again SecurityMode
		if err := again.UnmarshalJSON(encoded); err != nil || again != m {
			t.Fatalf("mode round trip: %d, %v; want %d", again, err, m)
		}
	})
}

func FuzzBytes(f *testing.F) {
	for _, s := range []string{`[]`, `[0,127,255,-128,-1]`, `null`, `[256]`, `[-129]`, `"abc"`, `{}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var b Bytes
		if err := b.UnmarshalJSON(data); err != nil {
			return
		}
		encoded, err := b.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var again Bytes
		if err := again.UnmarshalJSON(encoded); err != nil || !bytes.Equal(again, b) {
			t.Fatalf("bytes round trip: %v, %v; want %v", again, err, b)
		}
	})
}
