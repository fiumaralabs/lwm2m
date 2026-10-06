package oscore

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

func h(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.ReplaceAll(s, " ", ""), "0x"))
	if err != nil {
		panic(err)
	}
	return b
}

func decode(t *testing.T, b []byte) message.Message {
	t.Helper()
	m := message.Message{Options: make(message.Options, 0, 16)}
	if _, err := coder.DefaultCoder.Decode(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func encode(t *testing.T, m message.Message) []byte {
	t.Helper()
	n, _ := coder.DefaultCoder.Size(m)
	b := make([]byte, n)
	if _, err := coder.DefaultCoder.Encode(m, b); err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	secret = h("0102030405060708090a0b0c0d0e0f10")
	salt   = h("9e7ca92223786340")
)

// Proves: OSC-02
// RFC 8613 Appendix C.1-C.3: HKDF-SHA-256 key derivation for
// AES-CCM-16-64-128 with and without Master Salt and with ID Context, for
// both roles, plus the nonces for Partial IV 0.
func TestKeyDerivationVectors(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		p                     Params
		sk, rk, iv, snon, rno string
	}{
		{"C.1 client", Params{MasterSecret: secret, MasterSalt: salt, SenderID: []byte{}, RecipientID: h("01")},
			"f0910ed7295e6ad4b54fc793154302ff", "ffb14e093c94c9cac9471648b4f98710", "4622d4dd6d944168eefb54987c", "4622d4dd6d944168eefb54987c", "4722d4dd6d944169eefb54987c"},
		{"C.1 server", Params{MasterSecret: secret, MasterSalt: salt, SenderID: h("01"), RecipientID: []byte{}},
			"ffb14e093c94c9cac9471648b4f98710", "f0910ed7295e6ad4b54fc793154302ff", "4622d4dd6d944168eefb54987c", "4722d4dd6d944169eefb54987c", "4622d4dd6d944168eefb54987c"},
		{"C.2 client", Params{MasterSecret: secret, SenderID: h("00"), RecipientID: h("01")},
			"321b26943253c7ffb6003b0b64d74041", "e57b5635815177cd679ab4bcec9d7dda", "be35ae297d2dace910c52e99f9", "bf35ae297d2dace910c52e99f9", "bf35ae297d2dace810c52e99f9"},
		{"C.2 server", Params{MasterSecret: secret, SenderID: h("01"), RecipientID: h("00")},
			"e57b5635815177cd679ab4bcec9d7dda", "321b26943253c7ffb6003b0b64d74041", "be35ae297d2dace910c52e99f9", "bf35ae297d2dace810c52e99f9", "bf35ae297d2dace910c52e99f9"},
		{"C.3 client", Params{MasterSecret: secret, MasterSalt: salt, SenderID: []byte{}, RecipientID: h("01"), IDContext: h("37cbf3210017a2d3")},
			"af2a1300a5e95788b356336eeecd2b92", "e39a0c7c77b43f03b4b39ab9a268699f", "2ca58fb85ff1b81c0b7181b85e", "2ca58fb85ff1b81c0b7181b85e", "2da58fb85ff1b81d0b7181b85e"},
		{"C.3 server", Params{MasterSecret: secret, MasterSalt: salt, SenderID: h("01"), RecipientID: []byte{}, IDContext: h("37cbf3210017a2d3")},
			"e39a0c7c77b43f03b4b39ab9a268699f", "af2a1300a5e95788b356336eeecd2b92", "2ca58fb85ff1b81c0b7181b85e", "2da58fb85ff1b81d0b7181b85e", "2ca58fb85ff1b81c0b7181b85e"},
	} {
		c, err := New(tc.p)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		for what, pair := range map[string][2][]byte{
			"sender key":      {c.SenderKey, h(tc.sk)},
			"recipient key":   {c.RecipientKey, h(tc.rk)},
			"common IV":       {c.CommonIV, h(tc.iv)},
			"sender nonce":    {c.Nonce(tc.p.SenderID, []byte{0}), h(tc.snon)},
			"recipient nonce": {c.Nonce(tc.p.RecipientID, []byte{0}), h(tc.rno)},
		} {
			if !bytes.Equal(pair[0], pair[1]) {
				t.Errorf("%s %s = %x, want %x", tc.name, what, pair[0], pair[1])
			}
		}
	}
}

// Proves: OSC-02
// RFC 8613 §5.4 AAD example and Appendix C.4-C.6: protected requests,
// byte-exact, and their verification by the server context.
func TestRequestVectors(t *testing.T) {
	c1 := Params{MasterSecret: secret, MasterSalt: salt, SenderID: []byte{}, RecipientID: h("01")}
	c2 := Params{MasterSecret: secret, SenderID: h("00"), RecipientID: h("01")}
	c3 := Params{MasterSecret: secret, MasterSalt: salt, SenderID: []byte{}, RecipientID: h("01"), IDContext: h("37cbf3210017a2d3"), SendKIDContext: true}
	cc2, _ := New(c2)
	if got := cc2.AAD(h("00"), h("25")); !bytes.Equal(got, h("8368456e63727970743040498501810a4100412540")) {
		t.Fatalf("§5.4 AAD %x", got)
	}
	for _, tc := range []struct {
		name         string
		p            Params
		plain, prot  string
		aad, nonceTV string
	}{
		{"C.4", c1, "44015d1f00003974396c6f63616c686f737483747631",
			"44025d1f00003974396c6f63616c686f7374620914ff612f1092f1776f1c1668b3825e",
			"8368456e63727970743040488501810a40411440", "4622d4dd6d944168eefb549868"},
		{"C.5", c2, "440171c30000b932396c6f63616c686f737483747631",
			"440271c30000b932396c6f63616c686f737463091400ff4ed339a5a379b0b8bc731fffb0",
			"8368456e63727970743040498501810a4100411440", "bf35ae297d2dace910c52e99ed"},
		{"C.6", c3, "44012f8eef9bbf7a396c6f63616c686f737483747631",
			"44022f8eef9bbf7a396c6f63616c686f73746b19140837cbf3210017a2d3ff72cd7273fd331ac45cffbe55c3",
			"8368456e63727970743040488501810a40411440", "2ca58fb85ff1b81c0b7181b84a"},
	} {
		cl, err := New(tc.p)
		if err != nil {
			t.Fatal(err)
		}
		cl.SetSequence(20)
		if got := cl.AAD(tc.p.SenderID, []byte{0x14}); !bytes.Equal(got, h(tc.aad)) {
			t.Errorf("%s AAD %x", tc.name, got)
		}
		if got := cl.Nonce(tc.p.SenderID, []byte{0x14}); !bytes.Equal(got, h(tc.nonceTV)) {
			t.Errorf("%s nonce %x", tc.name, got)
		}
		prot, x, err := cl.ProtectRequest(decode(t, h(tc.plain)))
		if err != nil {
			t.Fatal(err)
		}
		if got := encode(t, prot); !bytes.Equal(got, h(tc.prot)) {
			t.Errorf("%s protected\n got %x\nwant %s", tc.name, got, tc.prot)
		}
		if x.RequestPIV() != 20 || cl.Sequence() != 21 {
			t.Errorf("%s sequence not advanced", tc.name)
		}
		srv, _ := New(tc.p.Reverse())
		in, _, err := srv.UnprotectRequest(decode(t, h(tc.prot)))
		if err != nil {
			t.Fatalf("%s verify: %v", tc.name, err)
		}
		if got := encode(t, in); !bytes.Equal(got, h(tc.plain)) {
			t.Errorf("%s decrypted %x", tc.name, got)
		}
	}
}

// Proves: OSC-02
// RFC 8613 Appendix C.7 and C.8: responses to the C.4 request without and
// with a Partial IV, byte-exact, verified by the client.
func TestResponseVectors(t *testing.T) {
	p := Params{MasterSecret: secret, MasterSalt: salt, SenderID: []byte{}, RecipientID: h("01")}
	plainResp := "64455d1f00003974ff48656c6c6f20576f726c6421"
	for _, tc := range []struct {
		name   string
		newPIV bool
		prot   string
	}{
		{"C.7", false, "64445d1f0000397490ffdbaad1e9a7e7b2a813d3c31524378303cdafae119106"},
		{"C.8", true, "64445d1f00003974920100ff4d4c13669384b67354b2b6175ff4b8658c666a6cf88e"},
	} {
		cl, _ := New(p)
		cl.SetSequence(20)
		_, x, err := cl.ProtectRequest(decode(t, h("44015d1f00003974396c6f63616c686f737483747631")))
		if err != nil {
			t.Fatal(err)
		}
		srv, _ := New(p.Reverse())
		_, sx, err := srv.UnprotectRequest(decode(t, h("44025d1f00003974396c6f63616c686f7374620914ff612f1092f1776f1c1668b3825e")))
		if err != nil {
			t.Fatal(err)
		}
		prot, err := srv.ProtectResponse(decode(t, h(plainResp)), sx, tc.newPIV)
		if err != nil {
			t.Fatal(err)
		}
		if got := encode(t, prot); !bytes.Equal(got, h(tc.prot)) {
			t.Errorf("%s protected\n got %x\nwant %s", tc.name, got, tc.prot)
		}
		in, err := cl.UnprotectResponse(decode(t, h(tc.prot)), x)
		if err != nil {
			t.Fatalf("%s verify: %v", tc.name, err)
		}
		if got := encode(t, in); !bytes.Equal(got, h(plainResp)) {
			t.Errorf("%s decrypted %x", tc.name, got)
		}
	}
}

func pair(t *testing.T, p Params) (client, server *Context) {
	t.Helper()
	c, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(p.Reverse())
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func get1(path string, opts ...message.Option) message.Message {
	m := message.Message{Code: codes.GET, Token: []byte{1, 2}, MessageID: 7, Type: message.Confirmable}
	m.Options = append(m.Options, opts...)
	m.Options = append(m.Options, message.Option{ID: message.URIPath, Value: []byte(path)})
	return m
}

// Proves: OSC-02
// Replay protection (§7.4, default 32-entry window of §3.2.2), integrity
// (§8.2 step 6), header decoding errors (§6.1, §8.2 step 2), the
// Appendix B.1.2 window lower limit, and the sequence number limit
// (§7.2.1).
func TestReplayAndErrors(t *testing.T) {
	cl, srv := pair(t, Params{MasterSecret: secret, SenderID: h("0a"), RecipientID: h("0b")})
	var msgs []message.Message
	for range 40 {
		m, _, err := cl.ProtectRequest(get1("3"))
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	// Out of order within the window is fine; replays are not.
	for _, i := range []int{5, 3, 4, 39} {
		if _, _, err := srv.UnprotectRequest(msgs[i]); err != nil {
			t.Fatalf("piv %d: %v", i, err)
		}
	}
	for _, i := range []int{5, 3, 39, 7} { // 7 is more than 31 below 39
		if _, _, err := srv.UnprotectRequest(msgs[i]); !errors.Is(err, ErrReplay) {
			t.Fatalf("piv %d: %v, want replay", i, err)
		}
	}
	if _, _, err := srv.UnprotectRequest(msgs[20]); err != nil {
		t.Fatal(err)
	}
	// Tampered ciphertext.
	m, _, _ := cl.ProtectRequest(get1("3"))
	m.Payload = append([]byte(nil), m.Payload...)
	m.Payload[0] ^= 1
	if _, _, err := srv.UnprotectRequest(m); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
	// A failed decryption does not consume the Partial IV... but the
	// genuine message with that PIV still verifies.
	m.Payload[0] ^= 1
	if _, _, err := srv.UnprotectRequest(m); err != nil {
		t.Fatalf("genuine after tamper: %v", err)
	}
	// Reserved flag bits, reserved n, truncated values.
	for _, v := range [][]byte{{0x20}, {0x06, 1, 2, 3, 4, 5, 6}, {0x03, 1}, {0x11, 1, 5, 0}, {0x01, 1, 9}} {
		if _, err := ParseHeader(v); !errors.Is(err, ErrDecode) {
			t.Errorf("header %x: %v", v, err)
		}
	}
	// Requests need both kid and Partial IV.
	m, _, _ = cl.ProtectRequest(get1("3"))
	for i := range m.Options {
		if m.Options[i].ID == OptionOSCORE {
			m.Options[i].Value = []byte{0x01, 0x30} // PIV, no kid
		}
	}
	if _, _, err := srv.UnprotectRequest(m); !errors.Is(err, ErrDecode) {
		t.Fatalf("no kid: %v", err)
	}
	// Nested OSCORE is refused (§4.1.3.7).
	if _, _, err := cl.ProtectRequest(get1("3", message.Option{ID: OptionOSCORE})); !errors.Is(err, ErrNested) {
		t.Fatalf("nested: %v", err)
	}
	// Appendix B.1.2: after the Echo exchange the server sets the verified
	// PIV as the lower limit of the window.
	a, _, _ := cl.ProtectRequest(get1("3"))
	b, xb, _ := cl.ProtectRequest(get1("3"))
	srv.ResetReplayWindow(xb.RequestPIV())
	if _, _, err := srv.UnprotectRequest(a); !errors.Is(err, ErrReplay) {
		t.Fatalf("below the lower limit: %v", err)
	}
	if _, _, err := srv.UnprotectRequest(b); !errors.Is(err, ErrReplay) {
		t.Fatalf("the lower limit itself: %v", err)
	}
	cl.SetSequence(MaxSequence + 1)
	if _, _, err := cl.ProtectRequest(get1("3")); !errors.Is(err, ErrSequence) {
		t.Fatalf("exhausted: %v", err)
	}
}

// Proves: OSC-02
// Inner and Outer options (Figure 5), Outer Code (§4.2) and Observe
// (§4.1.3.5): Uri-Path, Uri-Query, Content-Format and Echo are encrypted,
// Uri-Host stays outside; an Observe registration is sent as FETCH and
// its notifications carry fresh Partial IVs, are ordered by them and
// replays are refused (§7.4.1).
func TestOptionClassesAndObserve(t *testing.T) {
	cl, srv := pair(t, Params{MasterSecret: secret, SenderID: h("0a"), RecipientID: h("0b")})
	req := get1("3",
		message.Option{ID: message.URIHost, Value: []byte("h")},
		message.Option{ID: message.Observe, Value: nil},
	)
	req.Options = append(req.Options, message.Option{ID: message.URIQuery, Value: []byte("pmin=1")}, message.Option{ID: OptionEcho, Value: []byte{9, 9}})
	req.Options = sorted(req.Options)
	prot, x, err := cl.ProtectRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if prot.Code != codes.Code(5) || !x.Observe() {
		t.Fatalf("outer code %v observe %v", prot.Code, x.Observe())
	}
	for _, o := range prot.Options {
		if o.ID != message.URIHost && o.ID != message.Observe && o.ID != OptionOSCORE {
			t.Fatalf("option %d leaked outside", o.ID)
		}
	}
	in, sx, err := srv.UnprotectRequest(prot)
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := in.Options.Queries(); len(q) != 1 || q[0] != "pmin=1" || in.Code != codes.GET {
		t.Fatalf("decrypted %v", in)
	}
	if e, ok := get(in.Options, OptionEcho); !ok || !bytes.Equal(e.Value, []byte{9, 9}) {
		t.Fatal("Echo lost")
	}
	notify := func(seq byte) message.Message {
		m := message.Message{Code: codes.Content, Token: []byte{1, 2}, Type: message.Confirmable, Payload: []byte{seq}}
		m.Options = message.Options{{ID: message.Observe, Value: []byte{seq}}, {ID: message.ContentFormat, Value: []byte{0x2d, 0x16}}}
		p, err := srv.ProtectResponse(m, sx, false)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	n1, n2, n3 := notify(1), notify(2), notify(3)
	if h1, _ := ParseHeader(mustOpt(t, n1)); h1.PIV != nil {
		t.Fatal("first notification may reuse the request nonce")
	}
	if h2, _ := ParseHeader(mustOpt(t, n2)); h2.PIV == nil || n2.Code != codes.Content {
		t.Fatal("later notifications need a Partial IV and outer 2.05")
	}
	got1, err := cl.UnprotectResponse(n1, x)
	if err != nil {
		t.Fatal(err)
	}
	got3, err := cl.UnprotectResponse(n3, x)
	if err != nil {
		t.Fatal(err)
	}
	o1, _ := got1.Options.Observe()
	o3, _ := got3.Options.Observe()
	if o1 != 0 || o3 == 0 || got3.Payload[0] != 3 {
		t.Fatalf("observe %d %d", o1, o3)
	}
	if _, err := cl.UnprotectResponse(n2, x); !errors.Is(err, ErrReplay) {
		t.Fatalf("older notification: %v", err)
	}
	if _, err := cl.UnprotectResponse(n1, x); !errors.Is(err, ErrReplay) {
		t.Fatalf("second notification without PIV: %v", err)
	}
}

func mustOpt(t *testing.T, m message.Message) []byte {
	t.Helper()
	v, err := optionValue(m)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Proves: OSC-02
// Other AEAD and HKDF algorithms MAY be supported: every listed AEAD
// round-trips; an ID longer than nonce-6 bytes and unknown algorithms are
// refused (§3.3).
func TestAlgorithms(t *testing.T) {
	for alg := range aeads {
		for _, kdf := range []int{0, HMAC256, HMAC384, HMAC512, HKDFSHA512} {
			cl, srv := pair(t, Params{MasterSecret: secret, SenderID: h("01"), RecipientID: []byte{}, AEAD: alg, HKDF: kdf})
			m, _, err := cl.ProtectRequest(get1("x"))
			if err != nil {
				t.Fatal(alg, err)
			}
			if _, _, err := srv.UnprotectRequest(m); err != nil {
				t.Fatal(alg, kdf, err)
			}
		}
	}
	for _, p := range []Params{
		{MasterSecret: secret, SenderID: make([]byte, 8)},
		{MasterSecret: secret, SenderID: make([]byte, 2), AEAD: AESCCM64_64_128},
		{MasterSecret: secret, AEAD: 99},
		{MasterSecret: secret, HKDF: 99},
		{SenderID: []byte{1}},
	} {
		if _, err := New(p); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}
