package server

import (
	"context"
	"net"
	"testing"
)

type exchangePeer struct{ addr string }

func (p exchangePeer) Exchange(context.Context, *Message) (*Message, error) { return nil, nil }
func (p exchangePeer) Identity() Identity                                   { return Identity{Mode: ModeNoSec, Addr: p.addr} }
func (p exchangePeer) RemoteAddr() net.Addr                                 { return &net.UDPAddr{IP: net.ParseIP(p.addr), Port: 5683} }
func (p exchangePeer) Binding() string                                      { return "U" }

func TestRegisterExchangeIdentity(t *testing.T) {
	one := uint16(0)
	two := uint16(1)
	m := &Message{MessageID: &one, Token: []byte{1}}
	peer := exchangePeer{addr: "192.0.2.1"}
	fingerprint := [32]byte{42}
	first := registerExchange(peer, m, fingerprint)
	if first == nil {
		t.Fatal("missing discriminator for MID zero")
	}
	if got := registerExchange(peer, m, fingerprint); got == nil || *first != *got {
		t.Fatal("same exchange differs")
	}
	m.MessageID = &two
	if got := registerExchange(peer, m, fingerprint); got == nil || *first == *got {
		t.Fatal("message ID ignored")
	}
	m.MessageID = &one
	m.Token = []byte{2}
	if got := registerExchange(peer, m, fingerprint); got == nil || *first == *got {
		t.Fatal("token ignored")
	}
	m.Token = []byte{1}
	if got := registerExchange(exchangePeer{addr: "192.0.2.2"}, m, fingerprint); got == nil || *first == *got {
		t.Fatal("peer ignored")
	}
	m.MessageID = nil
	if got := registerExchange(peer, m, fingerprint); got != nil {
		t.Fatal("missing MID must not yield an exchange")
	}
}
