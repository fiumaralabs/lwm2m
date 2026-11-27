package sms

import (
	"bytes"
	"testing"
)

func FuzzParseWAPPush(f *testing.F) {
	udh := []byte{5, 4, 0x0b, 0x84, 0xc0, 2}
	f.Add(udh, []byte{1, 6, 3, 0xc4, 0xaf, 0x9a, 0x40, 1, 0, 1})
	f.Add(udh, []byte{1, 6, 0})
	f.Add([]byte{5, 4, 0, 80, 0, 0}, []byte{1, 6, 3, 0xc4, 0xaf, 0x9a})
	f.Fuzz(func(t *testing.T, udh, data []byte) {
		payload, err := ParseWAPPush(SMS{UDH: udh, Data: data})
		if err != nil {
			return
		}
		u, d := WAPPush(payload)
		got, err := ParseWAPPush(SMS{UDH: u, Data: d})
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("WAP Push round trip: got %x, want %x, err %v", got, payload, err)
		}
	})
}
