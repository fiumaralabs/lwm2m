package dtlscoap

import "testing"

func TestConnectionKeyMatchesCallbackConnection(t *testing.T) {
	pc := &packetConn{}
	c := &Conn{pc: pc}
	if c.ConnectionKey() != pc {
		t.Fatal("connection key differs from callback connection")
	}
}
