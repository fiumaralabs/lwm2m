package server

import (
	"bytes"
	"sync"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

// maxBlock1Body bounds a reassembled Block1 request; larger is 4.13.
// ponytail: fixed 1 MiB, Send and Register bodies are far smaller; make it
// a Config field if a use needs more.
const maxBlock1Body = 1 << 20

// block1Lifetime is EXCHANGE_LIFETIME: an unfinished transfer older than
// that is dropped.
const block1Lifetime = 247 * time.Second

// block1Key identifies one Block1 transfer: the peer's connection, method,
// Uri-Path and Request-Tag (RFC 7959 §2.3, RFC 9175 §3). The token is not
// part of it: every block is its own exchange and may carry a new token,
// which Zephyr does. go-coap reassembles by token, so it passed the last
// block of a 4 KiB Send on its own (Zephyr interop test_blockwise_4).
type block1Key struct {
	cc   *client.Conn
	code codes.Code
	path string
	tag  string
}

type block1Transfer struct {
	body    []byte
	lastNum int64
	expires time.Time
}

type block1Assembler struct {
	mu sync.Mutex
	m  map[block1Key]*block1Transfer
}

// handle runs before go-coap's blockwise layer. It answers a non-final
// Block1 request with 2.31 Continue itself and returns true. For the final
// block it puts the whole body into req, keeps its Block1 option (go-coap
// then passes the request through and serveCoAP echoes the option), and
// returns false. Anything else is left alone.
func (a *block1Assembler) handle(req *pool.Message, cc *client.Conn) bool {
	switch req.Code() {
	case codes.POST, codes.PUT, codeFETCH, codeIPATCH:
	default:
		return false
	}
	opt, err := req.GetOptionUint32(message.Block1)
	if err != nil {
		return false
	}
	szx, num, more, err := blockwise.DecodeBlockOption(opt)
	if err != nil {
		return false
	}
	if num == 0 && !more {
		return false // a single block: nothing to reassemble
	}
	path, _ := req.Path()
	tag, _ := req.GetOptionBytes(optRequestTag)
	key := block1Key{cc: cc, code: req.Code(), path: path, tag: string(tag)}
	payload, err := req.ReadBody()
	if err != nil {
		return a.reply(req, cc, codes.BadRequest, 0)
	}
	now := time.Now()

	a.mu.Lock()
	if a.m == nil {
		a.m = map[block1Key]*block1Transfer{}
	}
	t := a.m[key]
	if num == 0 {
		for k, old := range a.m {
			if now.After(old.expires) {
				delete(a.m, k)
			}
		}
		t = &block1Transfer{lastNum: -1}
		a.m[key] = t
	}
	off := num * szx.Size()
	switch {
	case t == nil:
		a.mu.Unlock()
		return a.reply(req, cc, codes.RequestEntityIncomplete, 0) // 4.08: no block 0 seen
	case num == t.lastNum && more:
		a.mu.Unlock() // retransmitted block whose 2.31 was lost
		return a.reply(req, cc, codes.Continue, opt)
	case off != int64(len(t.body)):
		delete(a.m, key)
		a.mu.Unlock()
		return a.reply(req, cc, codes.RequestEntityIncomplete, 0)
	case len(t.body)+len(payload) > maxBlock1Body:
		delete(a.m, key)
		a.mu.Unlock()
		return a.reply(req, cc, codes.RequestEntityTooLarge, 0)
	}
	t.body = append(t.body, payload...)
	t.lastNum, t.expires = num, now.Add(block1Lifetime)
	if more {
		a.mu.Unlock()
		return a.reply(req, cc, codes.Continue, opt)
	}
	delete(a.m, key)
	a.mu.Unlock()
	req.SetBody(bytes.NewReader(t.body))
	req.Remove(message.Size1)
	return false
}

// reply answers req directly (piggybacked on the ACK of a CON) and
// releases it. block, when non-zero, is echoed as the Block1 option.
func (a *block1Assembler) reply(req *pool.Message, cc *client.Conn, code codes.Code, block uint32) bool {
	resp := cc.AcquireMessage(cc.Context())
	resp.SetCode(code)
	resp.SetToken(req.Token())
	if req.Type() == message.Confirmable {
		resp.SetType(message.Acknowledgement)
		resp.SetMessageID(req.MessageID())
	} else {
		resp.SetType(message.NonConfirmable)
		resp.SetMessageID(message.GetMID())
	}
	if block != 0 {
		resp.SetOptionUint32(message.Block1, block)
	}
	_ = cc.Session().WriteMessage(resp)
	cc.ReleaseMessage(resp)
	cc.ReleaseMessage(req)
	return true
}
