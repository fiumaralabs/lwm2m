package cose

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

// Vectors from https://github.com/cose-wg/Examples (The Unlicense), commit
// 53c9d634333bb4f529d78f5980fffa2667ee2c12. All use the key "our-secret",
// k = hJtXIZ2uSN5kbQfbtTNWbg, plaintext "This is the content.", and the IV
// from rng_stream.
var vectors = []struct {
	file          string
	alg           int64
	iv, ext, cbor string
	fail          bool
}{
	{"aes-ccm-examples/aes-ccm-enc-01.json", AESCCM16_64_128, "89F52F65A1C580933B5261A72F", "",
		"D08343A1010AA1054D89F52F65A1C580933B5261A72F581C6899DA0A132BD2D2B9B10915743EE1F7B92A4680E7C51BDBC1B320EA", false},
	{"aes-gcm-examples/aes-gcm-enc-01.json", A128GCM, "02D1F7E6F26C43D4868D87CE", "",
		"D08343A10101A1054C02D1F7E6F26C43D4868D87CE582460973A94BB2898009EE52ECFD9AB1DD25867374B162E2C03568B41F57C3CC16F9166250A", false},
	{"encrypted-tests/enc-pass-02.json", A128GCM, "02D1F7E6F26C43D4868D87CE", "0011bbcc22dd4455dd220099",
		"D08343A10101A1054C02D1F7E6F26C43D4868D87CE582460973A94BB2898009EE52ECFD9AB1DD25867374B1DC3A143880CA2883A5630DA08AE1E6E", false},
	// Despite its name a failure test: protected changed to h'A0' after sealing.
	{"encrypted-tests/enc-pass-01.json (ChangeProtected)", A128GCM, "", "",
		"D08341A0A20101054C02D1F7E6F26C43D4868D87CE582460973A94BB2898009EE52ECFD9AB1DD25867374B24BEE54AA5D797C8DC845929ACAA47EF", true},
	{"encrypted-tests/enc-fail-02.json (ChangeTag)", A128GCM, "", "",
		"D08343A10101A1054C02D1F7E6F26C43D4868D87CE582460973A94BB2898009EE52ECFD9AB1DD25867374B162E2C03568B41F57C3CC16F9166250B", true},
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Proves: MQTT-11
func TestCOSEWGVectors(t *testing.T) {
	key, _ := base64.RawURLEncoding.DecodeString("hJtXIZ2uSN5kbQfbtTNWbg")
	pt := []byte("This is the content.")
	for _, v := range vectors {
		t.Run(v.file, func(t *testing.T) {
			want := unhex(t, v.cbor)
			ext := unhex(t, v.ext)
			if v.iv != "" {
				got, err := Seal(v.alg, key, nil, unhex(t, v.iv), pt, ext)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(append([]byte{0xd0}, got...), want) {
					t.Fatalf("Seal = %X\nwant  %X", got, want[1:])
				}
			}
			got, kid, err := Open(v.alg, key, want, ext)
			if v.fail {
				if !errors.Is(err, ErrDecrypt) {
					t.Fatalf("Open = %v, want ErrDecrypt", err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, pt) || kid != nil {
				t.Fatalf("Open = %q, %x, %v", got, kid, err)
			}
		})
	}
}

// Proves: MQTT-11
func TestOpenRejects(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	iv := bytes.Repeat([]byte{1}, 13)
	msg, err := Seal(AESCCM16_64_128, key, []byte("kid1"), iv, []byte("hi"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if pt, kid, err := Open(AESCCM16_64_128, key, msg, nil); err != nil || string(pt) != "hi" || string(kid) != "kid1" {
		t.Fatalf("round trip: %q %q %v", pt, kid, err)
	}
	if _, _, err := Open(A128GCM, key, msg, nil); err == nil {
		t.Error("algorithm substitution accepted")
	}
	if _, _, err := Open(AESCCM16_64_128, key, msg, []byte("other aad")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong external AAD: %v", err)
	}
	if _, _, err := Open(AESCCM16_64_128, bytes.Repeat([]byte{8}, 16), msg, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong key: %v", err)
	}
	bad := bytes.Clone(msg)
	bad[len(bad)-3] ^= 1
	if _, _, err := Open(AESCCM16_64_128, key, bad, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered ciphertext: %v", err)
	}
	if _, err := Seal(AESCCM16_64_128, key[:15], nil, iv, nil, nil); err == nil {
		t.Error("short key accepted")
	}
	if _, err := Seal(AESCCM16_64_128, key, nil, iv[:12], nil, nil); err == nil {
		t.Error("short IV accepted")
	}
	if _, err := Seal(99, key, nil, iv, nil, nil); err == nil {
		t.Error("unknown algorithm accepted")
	}
}
