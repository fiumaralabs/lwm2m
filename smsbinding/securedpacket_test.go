package smsbinding

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// Proves: SMS-05
// AES-CMAC, the integrity algorithm of KID '02', reproduces the four
// RFC 4493 §4 test vectors (AES-128, the same examples as NIST SP 800-38B
// D.1).
func TestCMACRFC4493(t *testing.T) {
	blk, _ := aes.NewCipher(unhex("2b7e151628aed2a6abf7158809cf4f3c"))
	msg := unhex("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710")
	for n, want := range map[int]string{
		0:  "bb1d6929e95937287fa37d129b756746",
		16: "070a16b46b4d4144f79bdd9dd04a287c",
		40: "dfa66747de9ae63030ca32611497c827",
		64: "51f0bebf7e3b9d92fc49741779363cfe",
	} {
		if got := hex.EncodeToString(cmac(blk, msg[:n])); got != want {
			t.Errorf("CMAC(%d bytes) = %s, want %s", n, got, want)
		}
	}
}

var (
	aesKeys = PacketKeys{KIc: 0x10 | AlgAES, KID: 0x10 | AlgAES,
		Cipher: unhex("000102030405060708090a0b0c0d0e0f"), Auth: unhex("f0e0d0c0b0a090807060504030201000")}
	desKeys = PacketKeys{KIc: AlgTripleDES, KID: AlgTripleDES,
		Cipher: unhex("0123456789abcdef23456789abcdef01456789abcdef0123"), Auth: unhex("89abcdef0123456723456789abcdef0101234567fedcba98")}
)

func pair(k PacketKeys) (srv, card *SecuredPacket) {
	f := func(string) (PacketKeys, error) { return k, nil }
	return &SecuredPacket{Keys: f}, &SecuredPacket{Keys: f}
}

// decodeCommand checks a Command Packet against 31.115 Tbl 1 / 102 225
// Tbl 1 with the stdlib ciphers, independently of Open, and returns the
// Secured Data value.
func decodeCommand(t *testing.T, k PacketKeys, m SMS) []byte {
	t.Helper()
	if !bytes.Equal(m.UDH, []byte{0x70, 0x00}) || m.PID != 0x7F || m.DCS != 0xF6 {
		t.Fatalf("UDH %x (want CPI 70 00), TP-PID %#x (want 01 111111), DCS %#x (want class 2, 8-bit)", m.UDH, m.PID, m.DCS)
	}
	d := m.Data
	var blk cipher.Block
	var mac func([]byte) []byte
	if k.KIc&0x0F == AlgAES {
		blk, _ = aes.NewCipher(k.Cipher)
		a, _ := aes.NewCipher(k.Auth)
		mac = func(b []byte) []byte { return cmac(a, b)[:8] }
	} else {
		blk, _ = des.NewTripleDESCipher(k.Cipher)
		a, _ := des.NewTripleDESCipher(k.Auth)
		mac = func(b []byte) []byte {
			b = append(b, make([]byte, (8-len(b)%8)%8)...)
			cipher.NewCBCEncrypter(a, make([]byte, 8)).CryptBlocks(b, b)
			return b[len(b)-8:]
		}
	}
	cpl, chl := int(binary.BigEndian.Uint16(d)), int(d[2])
	if cpl != len(d)-2 || chl != 21 {
		t.Fatalf("CPL %d (len %d), CHL %d (want 21: SPI..CC with an 8-byte CC)", cpl, len(d), chl)
	}
	if d[3] != 0x16 || d[5] != k.KIc || d[6] != k.KID || !bytes.Equal(d[7:10], []byte{0xB2, 0x02, 0x03}) {
		t.Fatalf("SPI %x KIc %x KID %x TAR %x", d[3:5], d[5], d[6], d[7:10])
	}
	ct := d[10:]
	if len(ct)%blk.BlockSize() != 0 {
		t.Fatalf("ciphered part %d bytes", len(ct))
	}
	p := make([]byte, len(ct))
	cipher.NewCBCDecrypter(blk, make([]byte, blk.BlockSize())).CryptBlocks(p, ct) // ICV zero
	body := p[14:]
	if !bytes.Equal(mac(append(append(append([]byte{}, d[:10]...), p[:6]...), body...)), p[6:14]) {
		t.Fatal("CC does not cover CPL, CHL, SPI..PCNTR and the padded Secured Data")
	}
	sd := body[:len(body)-int(p[5])]
	v, err := parseBerTLV(0x05, sd)
	if err != nil {
		t.Fatalf("Secured Data %x: %v", sd, err)
	}
	return v
}

// Proves: SMS-05, SMS-03
// A full smartcard exchange through the SMS binding: the card's Register
// arrives as a Command Packet (CPI 70 00) from TP-OA "+12345678"; the
// 2.01 goes back as a Command Packet, not a Response Packet, to that TP-OA
// verbatim, class 2 with TP-PID 111111, SPI 16 (CC, ciphering, counter
// must be higher), TAR B2 02 03, AES-CBC (KIc 02) and AES-CMAC (KID 02)
// with a zero ICV, CC over CPL..PCNTR plus padded data, and the CoAP in a
// BER-TLV tagged 0x05 (A-19). The Execute /1/0/8 trigger and a server
// request too long for one SMS (CPI in the first part only) are also
// Command Packets. A replayed or older counter, a wrong TAR, a forged
// checksum, other key parameters and a clear-text message are all dropped
// without reply.
func TestSecuredPacketRoundTrip(t *testing.T) {
	srvSec, card := pair(aesKeys)
	b, smsc, srv := setup(t, Config{Security: srvSec, ResponseTimeout: time.Second})
	ctx := context.Background()
	const oa = "+12345678"
	reg := enc(t, coapwire.Frame{Type: message.Confirmable, MID: 3, Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: []string{"ep=uicc", "lt=600", "lwm2m=1.2", "b=S", "sms=12345678"},
		Token: []byte{7}, Format: ptr(lwm2m.FormatLinkFormat), Payload: []byte("</1/0>,</3/0>"),
	}})
	in, err := card.Seal(msisdn, reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeCommand(t, aesKeys, in); !bytes.Equal(got, reg) {
		t.Fatal("card packet does not carry the CoAP")
	}
	in.MSISDN = oa
	if err := b.Deliver(ctx, in); err != nil {
		t.Fatal(err)
	}
	out := smsc.next(t)
	if out.MSISDN != oa {
		t.Fatalf("TP-DA %q, want the incoming TP-OA %q", out.MSISDN, oa)
	}
	if f := frame(t, decodeCommand(t, aesKeys, out)); f.Msg.Code != codes.Created {
		t.Fatalf("reply %+v", f)
	}
	if _, ok := srv.Store().ByEndpoint("uicc"); !ok {
		t.Fatal("not registered")
	}
	out.MSISDN = msisdn
	if _, err := card.Open(out); err != nil {
		t.Fatal(err)
	}

	// Replay and an older counter: dropped silently.
	if err := b.Deliver(ctx, in); err != nil {
		t.Fatal(err)
	}
	smsc.none(t)
	older, _ := card.Seal(msisdn, reg)
	newer, _ := card.Seal(msisdn, reg)
	if _, err := srvSec.Open(newer); err != nil {
		t.Fatal(err)
	}
	if _, err := srvSec.Open(older); !errors.Is(err, ErrReplay) {
		t.Fatalf("older counter: %v", err)
	}

	// Wrong TAR with a valid checksum, forged checksum, wrong KIc, clear text.
	badTAR, _ := card.command(msisdn, reg, [3]byte{0xB2, 0x02, 0x04})
	if _, err := srvSec.Open(badTAR); err == nil {
		t.Fatal("TAR B2 02 04 accepted")
	}
	forged, _ := card.Seal(msisdn, reg)
	forged.Data[len(forged.Data)-1] ^= 1
	if _, err := srvSec.Open(forged); err == nil {
		t.Fatal("forged packet accepted")
	}
	k2 := aesKeys
	k2.KIc, k2.KID = 0x20|AlgAES, 0x20|AlgAES
	other := &SecuredPacket{Keys: func(string) (PacketKeys, error) { return k2, nil }}
	wrongKey, _ := other.Seal(msisdn, reg)
	if _, err := srvSec.Open(wrongKey); err == nil {
		t.Fatal("other key parameters accepted")
	}
	for _, m := range []SMS{badTAR, forged, wrongKey, {MSISDN: msisdn, Data: reg}} {
		_ = b.Deliver(ctx, m)
	}
	smsc.none(t)

	// The SMS trigger is a Command Packet too.
	if err := b.Trigger(ctx, msisdn, 0, false); err != nil {
		t.Fatal(err)
	}
	trig := smsc.next(t)
	if f := frame(t, decodeCommand(t, aesKeys, trig)); f.Msg.Code != codes.POST || f.Msg.Path != "/1/0/8" {
		t.Fatalf("trigger %+v", f)
	}

	// Concatenated Command Packet: CPI in the first part only.
	c, _ := b.Peer(msisdn)
	big := bytes.Repeat([]byte("y"), 250)
	go func() {
		_, _ = c.Exchange(ctx, &server.Message{Code: codes.PUT, Path: "/3/0/14", Token: []byte{5}, Payload: big})
	}()
	var joined SMS
	for i, done := 0, false; !done; i++ {
		p := smsc.next(t)
		if hasIE(p.UDH, 0x70) != (i == 0) || p.PID != 0x7F || p.DCS != 0xF6 || 1+len(p.UDH)+len(p.Data) > MaxUserData {
			t.Fatalf("part %d: UDH %x PID %x DCS %x", i, p.UDH, p.PID, p.DCS)
		}
		joined, done = b.reassemble("x", p)
	}
	if f := frame(t, decodeCommand(t, aesKeys, SMS{UDH: []byte{0x70, 0}, PID: 0x7F, DCS: 0xF6, Data: joined.Data})); !bytes.Equal(f.Msg.Payload, big) {
		t.Fatal("concatenated payload")
	}
}

// Proves: SMS-05
// Triple DES works in outer CBC with three keys (KIc/KID '09', 8-byte
// blocks, CBC-MAC checksum); single DES ('01', '0D') and two-key triple
// DES ('05') are refused on both Seal and Open, as are triple DES keys
// that are not three different keys.
func TestSecuredPacketDES(t *testing.T) {
	srvSec, card := pair(desKeys)
	m, err := card.Seal(msisdn, []byte("coap"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decodeCommand(t, desKeys, m), []byte("coap")) {
		t.Fatal("3DES packet")
	}
	if got, err := srvSec.Open(m); err != nil || string(got) != "coap" {
		t.Fatalf("open %q %v", got, err)
	}
	for _, alg := range []byte{0x01, 0x0D, 0x05} {
		k := desKeys
		k.KIc = alg
		s := &SecuredPacket{Keys: func(string) (PacketKeys, error) { return k, nil }}
		if _, err := s.Seal(msisdn, []byte("x")); !errors.Is(err, ErrSingleDES) {
			t.Errorf("KIc %#02x seal: %v", alg, err)
		}
		if _, err := s.Open(m); !errors.Is(err, ErrSingleDES) {
			t.Errorf("KIc %#02x open: %v", alg, err)
		}
	}
	k := desKeys
	k.Cipher = append(append([]byte{}, desKeys.Cipher[:16]...), desKeys.Cipher[:8]...) // K1 K2 K1
	if _, err := (&SecuredPacket{Keys: func(string) (PacketKeys, error) { return k, nil }}).Seal(msisdn, nil); err == nil {
		t.Fatal("two-key triple DES accepted")
	}
}

// Proves: SMS-05
// When the server policy asks for a PoR (SPI2 39: PoR by SMS-SUBMIT, CC,
// ciphered), the card's Response Packet (RPI 71 00, CC over 02 71 00
// RPL..status plus data, TAR and CNTR copied) is authenticated and
// reported, also when the UICC could not set UDHI. A Response Packet with
// a bad checksum, or one never asked for, is dropped.
func TestSecuredPacketPoR(t *testing.T) {
	k := aesKeys
	k.SPI2 = 0x39
	srvSec, card := pair(k)
	var got []byte
	srvSec.OnPoR = func(_ string, cntr uint64, status byte, data []byte) {
		got = append(counter(cntr), append([]byte{status}, data...)...)
	}
	cmd, err := srvSec.Seal(msisdn, []byte("req"))
	if err != nil || cmd.Data[4] != 0x39 {
		t.Fatalf("SPI2 %x %v", cmd.Data[3:5], err)
	}
	if _, err := card.Open(cmd); err != nil {
		t.Fatal(err)
	}
	cntr := card.rx[msisdn]
	por, err := card.ResponsePacket(msisdn, cntr, 0x00, []byte("ok"))
	if err != nil || !bytes.Equal(por.UDH, []byte{0x71, 0}) || !bytes.Equal(por.Data[3:6], []byte{0xB2, 2, 3}) || int(por.Data[2]) != 18 {
		t.Fatalf("PoR %x %v", por.Data, err)
	}
	if _, err := srvSec.Open(por); err == nil || !bytes.Equal(got, append(counter(cntr), 0, 'o', 'k')) {
		t.Fatalf("PoR not reported: %x %v", got, err)
	}
	got = nil
	noUDHI := SMS{MSISDN: msisdn, Data: append([]byte{2, 0x71, 0}, por.Data...)}
	if _, _ = srvSec.Open(noUDHI); got == nil {
		t.Fatal("PoR without UDHI not reported")
	}
	got = nil
	por.Data[len(por.Data)-1] ^= 1
	_, _ = srvSec.Open(por)
	unasked, _ := pair(aesKeys)
	unasked.OnPoR = srvSec.OnPoR
	_, _ = unasked.Open(noUDHI)
	if got != nil {
		t.Fatal("bad PoR reported")
	}
}
