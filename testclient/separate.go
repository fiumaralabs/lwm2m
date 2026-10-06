package testclient

import (
	"bytes"
	"context"

	"github.com/fiumaralabs/lwm2m"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// RespondSeparately sends a separate response (RFC 7252 §5.2.2) for the
// request with token tok, as CON or NON. Pair it with an Override that
// returns codes.Empty, which sends only the empty ACK.
func (c *Client) RespondSeparately(ctx context.Context, tok message.Token, code codes.Code, cf *lwm2m.ContentFormat, body []byte, confirmable bool) error {
	m := c.conn.AcquireMessage(ctx)
	defer c.conn.ReleaseMessage(m)
	m.SetCode(code)
	m.SetToken(tok)
	if cf != nil {
		m.SetContentFormat(message.MediaType(*cf))
	}
	if body != nil {
		m.SetBody(bytes.NewReader(body))
	}
	m.SetMessageID(c.conn.GetMessageID())
	if confirmable {
		m.SetType(message.Confirmable)
		return c.conn.WriteMessage(m) // waits for the server's ACK
	}
	m.SetType(message.NonConfirmable)
	return c.conn.Session().WriteMessage(m)
}
