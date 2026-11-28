package sqlite_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/transport/sms"
)

func TestSMSCounterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	open := func() *sqlite.Devices {
		s, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	keys := func(string) (sms.PacketKeys, error) {
		return sms.PacketKeys{KIc: 0x10 | sms.AlgAES, KID: 0x10 | sms.AlgAES, Cipher: []byte("0123456789abcdef"), Auth: []byte("fedcba9876543210")}, nil
	}
	secure := func(s *sqlite.Devices) *sms.SecuredPacket {
		return &sms.SecuredPacket{Keys: keys, Counters: s}
	}
	db := open()
	receiver := secure(db)
	sender := &sms.SecuredPacket{Keys: keys}
	packet, err := sender.Seal("123456", []byte("request"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = receiver.Open(packet); err != nil {
		t.Fatal(err)
	}
	first, err := receiver.Seal("123456", []byte("response"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	defer db.Close()
	receiver = secure(db)
	if _, err = receiver.Open(packet); !errors.Is(err, sms.ErrReplay) {
		t.Fatalf("replay after reopen: %v", err)
	}
	second, err := receiver.Seal("123456", []byte("response"))
	if err != nil {
		t.Fatal(err)
	}
	// A new recipient accepts both only if the transmitted counters increase.
	peer := &sms.SecuredPacket{Keys: keys}
	if _, err = peer.Open(first); err != nil {
		t.Fatal(err)
	}
	if _, err = peer.Open(second); err != nil {
		t.Fatalf("tx counter did not advance: %v", err)
	}
}

func TestSMSCounterAtomicAndBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	const n = 40
	values := make(chan uint64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := a
			if i%2 != 0 {
				db = b
			}
			v, err := db.NextSMSCounter("123", 1, n)
			if err != nil {
				errs <- err
			} else {
				values <- v
			}
		}(i)
	}
	wg.Wait()
	close(values)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := make(map[uint64]bool)
	for v := range values {
		if seen[v] {
			t.Errorf("duplicate counter %d", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d counters, want %d", len(seen), n)
	}
	if _, err := a.NextSMSCounter("123", 1, n); err == nil {
		t.Fatal("counter must block at maximum")
	}
	accepted, err := a.AcceptSMSCounter("123", 10)
	if err != nil || !accepted {
		t.Fatalf("accept: %v %v", accepted, err)
	}
	accepted, err = b.AcceptSMSCounter("123", 10)
	if err != nil || accepted {
		t.Fatalf("replay: %v %v", accepted, err)
	}
	accepted, err = b.AcceptSMSCounter("other", 10)
	if err != nil || !accepted {
		t.Fatalf("other MSISDN: %v %v", accepted, err)
	}
}

func TestSMSCounterStoreFailureClosed(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	keys := func(string) (sms.PacketKeys, error) {
		return sms.PacketKeys{KIc: 0x10 | sms.AlgAES, KID: 0x10 | sms.AlgAES, Cipher: []byte("0123456789abcdef"), Auth: []byte("fedcba9876543210")}, nil
	}
	sender := &sms.SecuredPacket{Keys: keys}
	packet, err := sender.Seal("123", []byte("request"))
	if err != nil {
		t.Fatal(err)
	}
	receiver := &sms.SecuredPacket{Keys: keys, Counters: db}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Open(packet); err == nil {
		t.Fatal("receive succeeded after store failure")
	}
	if _, err := receiver.Seal("123", []byte("reply")); err == nil {
		t.Fatal("send succeeded after store failure")
	}
}

func TestSMSProofOfReceiptAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := func(string) (sms.PacketKeys, error) {
		return sms.PacketKeys{KIc: 0x10 | sms.AlgAES, KID: 0x10 | sms.AlgAES, SPI2: 0x39, Cipher: []byte("0123456789abcdef"), Auth: []byte("fedcba9876543210")}, nil
	}
	server := &sms.SecuredPacket{Keys: keys, Counters: db}
	card := &sms.SecuredPacket{Keys: keys}
	packet, err := server.Seal("123", []byte("request"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := card.Open(packet); err != nil {
		t.Fatal(err)
	}
	counter, err := db.LastSMSCounter("123")
	if err != nil {
		t.Fatal(err)
	}
	por, err := card.ResponsePacket("123", counter, 0, []byte("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	called := false
	server = &sms.SecuredPacket{Keys: keys, Counters: db, OnPoR: func(_ string, got uint64, _ byte, data []byte) { called = got == counter && string(data) == "ok" }}
	_, _ = server.Open(por)
	if !called {
		t.Fatal("proof of receipt not accepted after reopen")
	}
}
