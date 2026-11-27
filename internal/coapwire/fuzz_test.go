package coapwire

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m/internal/vectors"
)

func coapSeeds(f *testing.F) {
	f.Helper()
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		f.Fatal(err)
	}
	for _, v := range vs {
		if v.ID == "spec-coap-sms-trigger-execute-1-0-8" {
			b, err := v.Payload()
			if err != nil {
				f.Fatal(err)
			}
			f.Add(b)
		}
	}
	f.Add([]byte{0x40, 0x01, 0, 1})
	f.Add([]byte{0x40})
}

func FuzzUnmarshal(f *testing.F) {
	coapSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Unmarshal(b)
		if err != nil {
			return
		}
		encoded, err := Marshal(m)
		if err != nil {
			return
		}
		again, err := Unmarshal(encoded)
		if err != nil {
			t.Fatalf("decode encoded frame: %v", err)
		}
		encodedAgain, err := Marshal(again)
		if err != nil || !bytes.Equal(encoded, encodedAgain) {
			t.Fatalf("unstable encoding: %x != %x: %v", encoded, encodedAgain, err)
		}
	})
}

func FuzzUnmarshalCoAP(f *testing.F) {
	coapSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := UnmarshalCoAP(b)
		if err != nil {
			return
		}
		encoded, err := MarshalCoAP(m)
		if err != nil {
			t.Fatalf("encode decoded message: %v", err)
		}
		again, err := UnmarshalCoAP(encoded)
		if err != nil {
			t.Fatalf("decode encoded message: %v", err)
		}
		encodedAgain, err := MarshalCoAP(again)
		if err != nil || !bytes.Equal(encoded, encodedAgain) {
			t.Fatalf("unstable encoding: %x != %x: %v", encoded, encodedAgain, err)
		}
	})
}
