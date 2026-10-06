package testclient

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/coapws"
	"github.com/gorilla/websocket"
	"github.com/plgd-dev/go-coap/v3/mux"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/tcp"
)

// DialWebSocket connects to a coap+ws:// or coaps+ws:// server (RFC 8323
// §4; ws:// and wss:// URLs) offering the "coap" subprotocol, and then
// behaves like Dial: the server's CSM must arrive within a second. tlsCfg
// is used for wss://.
func (t *TCPClient) DialWebSocket(url string, tlsCfg *tls.Config) (*http.Response, error) {
	d := websocket.Dialer{Subprotocols: []string{coapws.Subprotocol}, TLSClientConfig: tlsCfg, HandshakeTimeout: 5 * time.Second}
	ws, hr, err := d.Dial(url, nil)
	if err != nil {
		return hr, err
	}
	if ws.Subprotocol() != coapws.Subprotocol {
		_ = ws.Close()
		return hr, fmt.Errorf("testclient: server chose subprotocol %q", ws.Subprotocol())
	}
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(t.handle))
	conn, err := tcp.Client(coapws.NewConn(ws),
		options.WithMux(r),
		options.WithBlockwise(true, 0x6, 30*time.Second),
		options.WithCSMExchangeTimeout(time.Second),
		options.WithCloseSocket(),
	)
	if err != nil {
		_ = ws.Close()
		return hr, err
	}
	t.conn = conn
	return hr, nil
}
