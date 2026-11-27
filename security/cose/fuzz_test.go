package cose

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func FuzzOpen(f *testing.F) {
	key, err := base64.RawURLEncoding.DecodeString("hJtXIZ2uSN5kbQfbtTNWbg")
	if err != nil {
		f.Fatal(err)
	}
	for _, v := range vectors {
		if v.alg != A128GCM || v.ext != "" {
			continue
		}
		msg, err := hex.DecodeString(v.cbor)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(msg)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _, _ = Open(A128GCM, key, input, nil)
		// The fixed nonce and key are only used for isolated test messages.
		msg, err := Seal(A128GCM, key, []byte("kid1"), make([]byte, 12), input, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, kid, err := Open(A128GCM, key, msg, nil)
		if err != nil || !bytes.Equal(got, input) || !bytes.Equal(kid, []byte("kid1")) {
			t.Fatalf("seal/open mismatch: kid=%x err=%v", kid, err)
		}
	})
}
