package mqtt

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func FuzzUnmarshal(f *testing.F) {
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		f.Fatal(err)
	}
	for _, v := range vs {
		switch v.ID {
		case "spec-mqtt-read", "spec-mqtt-read-response", "spec-mqtt-bootstrap-request", "spec-mqtt-send":
			b, err := v.Payload()
			if err != nil {
				f.Fatal(err)
			}
			f.Add(b)
		}
	}
	f.Add([]byte{0xa1, 0x02, 0x01})
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Unmarshal(b)
		if err != nil {
			return
		}
		encoded, err := Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Unmarshal(encoded)
		if err != nil {
			t.Fatalf("decode encoded message: %v", err)
		}
		encodedAgain, err := Marshal(again)
		if err != nil || !bytes.Equal(encoded, encodedAgain) {
			t.Fatalf("unstable encoding: %x != %x: %v", encoded, encodedAgain, err)
		}
	})
}
