package fota_test

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

type harness struct {
	t     *testing.T
	srv   *server.Server
	mgr   *fota.Manager
	addr  string
	files *fota.FileServer
	coap  string // file server host:port
	ctx   context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t}
	models := server.NewModels(model.Default())
	h.srv = server.New(server.Config{
		OnEvent: func(e server.Event) { h.mgr.HandleEvent(e) }, RequestTimeout: 5 * time.Second,
		ExpiryCheck: time.Hour, Schema: models.Schema, Validator: models,
	})
	h.mgr = fota.New(h.srv)
	a, err := h.srv.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.addr = a.String()
	h.files = fota.NewFileServer()
	fa, err := h.files.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.coap = fa.String()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h.ctx = ctx
	t.Cleanup(func() {
		cancel()
		_ = h.files.Close()
		_ = h.srv.Close()
	})
	return h
}

// device registers a client with /1/0, /3/0 and /5/0 at addr (the server
// when "").
func (h *harness) device(cfg testclient.Config, fw testclient.FirmwareConfig, addr string) (*testclient.Client, *testclient.Firmware) {
	h.t.Helper()
	c := testclient.New(cfg)
	c.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(1))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(86400))
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	f := testclient.NewFirmware(c, fw)
	if addr == "" {
		addr = h.addr
	}
	if err := c.Dial(addr); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	if r, err := f.Register(h.ctx); err != nil || r.Code != 65 {
		h.t.Fatalf("register: %v %v", r, err)
	}
	return c, f
}

func image(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(uint64(n), 7))
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	b[0] = 0x7f // never a lone NUL
	return b
}

func ptr[T any](v T) *T { return &v }

// requests returns the client's requests with code and path.
func requests(c *testclient.Client, code uint8, path string) []testclient.Request {
	var out []testclient.Request
	for _, r := range c.Requests() {
		if uint8(r.Code) == code && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// block1 is one Block1 request the relay saw going to the client.
type block1 struct {
	token string
	num   int64
	more  bool
	size  int64
	size1 bool
	n     int // payload length
}

// relay is a UDP proxy between client and server that records the
// server's Block1 requests and can delay them by token.
type relay struct {
	ln, up *net.UDPConn
	mu     sync.Mutex
	client *net.UDPAddr
	seen   []block1
	delay  func(token []byte) time.Duration
}

func newRelay(t *testing.T, target string) *relay {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ta, _ := net.ResolveUDPAddr("udp", target)
	up, err := net.DialUDP("udp", nil, ta)
	if err != nil {
		t.Fatal(err)
	}
	r := &relay{ln: ln, up: up}
	t.Cleanup(func() { _ = ln.Close(); _ = up.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := ln.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r.mu.Lock()
			r.client = from
			r.mu.Unlock()
			_, _ = up.Write(buf[:n])
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := up.Read(buf)
			if err != nil {
				return
			}
			pkt := bytes.Clone(buf[:n])
			var d time.Duration
			m := message.Message{Options: make(message.Options, 0, 16)}
			if _, err := coder.DefaultCoder.Decode(pkt, &m); err == nil {
				if v, err := m.Options.GetUint32(message.Block1); err == nil && m.Code < 32 {
					szx, num, more, _ := blockwise.DecodeBlockOption(v)
					_, s1 := m.Options.GetUint32(message.Size1)
					r.mu.Lock()
					r.seen = append(r.seen, block1{string(m.Token), num, more, szx.Size(), s1 == nil, len(m.Payload)})
					if r.delay != nil {
						d = r.delay(m.Token)
					}
					r.mu.Unlock()
				}
			}
			r.mu.Lock()
			to := r.client
			r.mu.Unlock()
			if d > 0 {
				time.AfterFunc(d, func() { _, _ = ln.WriteToUDP(pkt, to) })
			} else {
				_, _ = ln.WriteToUDP(pkt, to)
			}
		}
	}()
	return r
}

func (r *relay) addr() string { return r.ln.LocalAddr().String() }

func (r *relay) blocks() []block1 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]block1(nil), r.seen...)
}
