package server_test

import (
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
)

// These are two distinct CoAP exchanges with identical registration content.
func TestRegisterContextAcrossMessageIDs(t *testing.T) {
	store := &contextRegistrationStore{MemoryStore: server.NewMemoryStore(), contexts: make(chan server.RegisterContext, 2)}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	conn, err := net.Dial("udp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, mid := range []byte{1, 2} {
		// CON POST, Uri-Path rd, Uri-Query ep=node, lt=600, lwm2m=1.1.
		packet := []byte{0x40, 0x02, 0, mid, 0xb2, 'r', 'd', 0x47, 'e', 'p', '=', 'n', 'o', 'd', 'e', 0x06, 'l', 't', '=', '6', '0', '0', 0x09, 'l', 'w', 'm', '2', 'm', '=', '1', '.', '1', 0xff, '<', '/', '3', '/', '0', '>'}
		if _, err := conn.Write(packet); err != nil {
			t.Fatal(err)
		}
		response := make([]byte, 1024)
		n, err := conn.Read(response)
		if err != nil {
			t.Fatal(err)
		}
		if n < 4 || response[1] != 65 || response[2] != 0 || response[3] != mid {
			t.Fatalf("unexpected response: %x", response[:n])
		}
	}
	first, second := <-store.contexts, <-store.contexts
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("message ID affected request fingerprint")
	}
	if first.Identity.Mode != server.ModeNoSec || first.Identity.Addr != conn.LocalAddr().String() {
		t.Fatalf("wrong NoSec identity: %+v", first.Identity)
	}
}
