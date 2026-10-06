package fota_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v3"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
)

// Proves: FW-02, FW-03, FW-04
// Pull over CoAP: the server skips a Package URI whose scheme /5/0/8 does
// not list, writes the coap:// one to /5/0/1 and the client downloads a
// 10 KiB image from FileServer the way Zephyr does: one token for every
// block, Size2: 0 in the request, 512-byte blocks, no query. Every block
// is a 2.05 with Size2 = image size and an ETag. Then Execute and Update
// Result 1.
func TestPullCoAP(t *testing.T) {
	h := newHarness(t)
	img := image(10 << 10)
	h.files.Add("/fw/app.bin", img)
	c, f := h.device(testclient.Config{Endpoint: "pull"}, testclient.FirmwareConfig{Version: "1.0", Protocols: []int64{0, 1}, Delivery: 0}, "")
	uri := "coap://" + h.coap + "/fw/app.bin"
	out, err := h.mgr.Run(h.ctx, "pull", fota.Job{URIs: []string{"https://example.com/app.bin", uri}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != fota.Pull || out.URI != uri || out.Result != fota.Success {
		t.Fatalf("%+v", out)
	}
	if w := requests(c, 3, "/5/0/1"); len(w) != 1 || string(w[0].Body) != uri {
		t.Fatalf("URI writes %v", w)
	}
	blocks := f.Pulled()
	if len(blocks) != 20 {
		t.Fatalf("%d blocks", len(blocks))
	}
	for i, b := range blocks {
		if !bytes.Equal(b.Token, blocks[0].Token) || b.Num != int64(i) || b.Size != 512 || b.Size2 != uint32(len(img)) || !b.HasETag {
			t.Fatalf("block %d: %+v", i, b)
		}
	}
	if len(requests(c, 2, "/5/0/2")) != 1 {
		t.Fatal("no Execute")
	}
}

// Proves: FW-02
// FileServer serves Block2 statelessly: a client taking a new token per
// block, asking for 256-byte blocks, gets the same image; a larger request
// is capped at 1024 bytes; the query string is ignored; a block past the
// end is 4.02, an unknown path 4.04, a non-GET 4.05; a small image comes
// whole without Block2.
func TestFileServerBlock2(t *testing.T) {
	fs := fota.NewFileServer()
	t.Cleanup(func() { _ = fs.Close() })
	a, err := fs.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	img := image(5000)
	fs.Add("fw/x.bin", img)
	fs.Add("/small", []byte("tiny"))
	cc, err := udp.Dial(a.String(), options.WithBlockwise(false, blockwise.SZX1024, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	get := func(code codes.Code, path string, szx blockwise.SZX, num int64, query ...string) (codes.Code, []byte, uint32, bool) {
		m := cc.AcquireMessage(ctx)
		defer cc.ReleaseMessage(m)
		m.SetCode(code)
		m.SetType(message.Confirmable)
		tok, _ := message.GetToken()
		m.SetToken(tok)
		_ = m.SetPath(path)
		for _, q := range query {
			m.AddQuery(q)
		}
		if szx != blockwise.SZXBERT {
			v, _ := blockwise.EncodeBlockOption(szx, num, false)
			m.SetOptionUint32(message.Block2, v)
		}
		r, err := cc.Do(m)
		if err != nil {
			t.Fatal(err)
		}
		defer cc.ReleaseMessage(r)
		body, _ := r.ReadBody()
		var more bool
		if v, err := r.Options().GetUint32(message.Block2); err == nil {
			s, n, mo, _ := blockwise.DecodeBlockOption(v)
			if n != num || (szx != blockwise.SZXBERT && s != min(szx, blockwise.SZX1024)) {
				t.Fatalf("block %d/%v answered as %d/%v", num, szx, n, s)
			}
			more = mo
		}
		size2, _ := r.Options().GetUint32(message.Size2)
		return r.Code(), body, size2, more
	}
	var got []byte
	for num := int64(0); ; num++ {
		code, body, size2, more := get(codes.GET, "/fw/x.bin", blockwise.SZX256, num, "tok=ignored")
		if code != codes.Content || size2 != 5000 {
			t.Fatalf("block %d: %v size2 %d", num, code, size2)
		}
		got = append(got, body...)
		if !more {
			break
		}
	}
	if !bytes.Equal(got, img) {
		t.Fatal("image differs")
	}
	if _, body, _, more := get(codes.GET, "/fw/x.bin", blockwise.SZXBERT, 0); len(body) != 1024 || !more {
		t.Fatalf("no Block2 in request: %d bytes, more %v", len(body), more)
	}
	if code, _, _, _ := get(codes.GET, "/fw/x.bin", blockwise.SZX1024, 5); code != codes.BadOption {
		t.Fatalf("past the end: %v", code)
	}
	if code, _, _, _ := get(codes.GET, "/nope", blockwise.SZX1024, 0); code != codes.NotFound {
		t.Fatalf("unknown: %v", code)
	}
	if code, _, _, _ := get(codes.PUT, "/fw/x.bin", blockwise.SZX1024, 0); code != codes.MethodNotAllowed {
		t.Fatalf("PUT: %v", code)
	}
	if code, body, _, _ := get(codes.GET, "/small", blockwise.SZXBERT, 0); code != codes.Content || string(body) != "tiny" {
		t.Fatalf("small: %v %q", code, body)
	}
	fs.Remove("/small")
	if code, _, _, _ := get(codes.GET, "/small", blockwise.SZXBERT, 0); code != codes.NotFound {
		t.Fatalf("removed: %v", code)
	}
}

// Proves: FW-02, FW-03
// Pull over CoAPs (PSK) and HTTPS: the server picks the scheme /5/0/8
// lists (1 CoAPS, 3 HTTPS) and the client downloads 8 KiB over it.
func TestPullSecure(t *testing.T) {
	h := newHarness(t)
	img := image(8 << 10)
	h.files.Add("/fw/s.bin", img)
	sa, err := h.files.ListenDTLS("127.0.0.1:0", &piondtls.Config{
		PSK:          func([]byte) ([]byte, error) { return []byte("0123456789abcdef"), nil },
		CipherSuites: []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
	})
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewTLSServer(h.files)
	t.Cleanup(web.Close)
	uris := []string{"coaps://" + sa.String() + "/fw/s.bin", web.URL + "/fw/s.bin", "coap://" + h.coap + "/fw/s.bin"}

	_, f := h.device(testclient.Config{Endpoint: "coaps"}, testclient.FirmwareConfig{Protocols: []int64{1}, Delivery: 2,
		PSKIdentity: "fw", PSKKey: []byte("0123456789abcdef"), Verify: verifyEqual(img)}, "")
	out, err := h.mgr.Run(h.ctx, "coaps", fota.Job{URIs: uris, Package: img})
	if err != nil || out.URI != uris[0] || out.Method != fota.Pull || len(f.Pulled()) != 16 {
		t.Fatalf("%+v %v (%d blocks)", out, err, len(f.Pulled()))
	}

	_, f = h.device(testclient.Config{Endpoint: "https"}, testclient.FirmwareConfig{Protocols: []int64{3}, Delivery: 0,
		HTTPClient: web.Client(), Verify: verifyEqual(img)}, "")
	out, err = h.mgr.Run(h.ctx, "https", fota.Job{URIs: uris})
	if err != nil || out.URI != uris[1] {
		t.Fatalf("%+v %v", out, err)
	}
	// The HTTP side serves ranges and an ETag like any file server.
	req, _ := http.NewRequest(http.MethodGet, uris[1], nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := web.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(b, img[:10]) || !strings.HasPrefix(resp.Header.Get("ETag"), `"`) {
		t.Fatalf("range: %d %q", resp.StatusCode, resp.Header.Get("ETag"))
	}
	if resp, _ := web.Client().Get(web.URL + "/missing"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: %d", resp.StatusCode)
	}
}

// verifyEqual is an integrity check: Update Result 5 unless pkg == want.
func verifyEqual(want []byte) func([]byte) int64 {
	return func(pkg []byte) int64 {
		if !bytes.Equal(pkg, want) {
			return 5
		}
		return 0
	}
}

// Proves: FW-03, FW-07
// The server never writes a Package URI whose protocol /5/0/8 does not
// list; unknown /5/0/8 values are ignored; a client without /5/0/8 is
// CoAP only. /5/0/9 Delivery Method decides between push and pull when
// both are possible, and a forced method the client does not offer is
// refused before anything is written.
func TestProtocolAndDeliveryChoice(t *testing.T) {
	h := newHarness(t)
	img := image(2000)
	h.files.Add("/fw/p.bin", img)
	coap := "coap://" + h.coap + "/fw/p.bin"

	c, _ := h.device(testclient.Config{Endpoint: "unknown-proto"}, testclient.FirmwareConfig{Protocols: []int64{0, 77}}, "")
	if ps, err := h.mgr.Protocols(h.ctx, "unknown-proto"); err != nil || len(ps) != 1 || ps[0] != fota.CoAP {
		t.Fatalf("protocols %v %v", ps, err)
	}
	if _, err := h.mgr.Run(h.ctx, "unknown-proto", fota.Job{URIs: []string{"https://example.com/p.bin", "coaps://example.com/p.bin"}}); !errors.Is(err, fota.ErrUnsupportedProtocol) {
		t.Fatalf("err %v", err)
	}
	if _, err := h.mgr.Run(h.ctx, "unknown-proto", fota.Job{Method: fota.Pull, URIs: []string{"ftp://x/y"}}); !errors.Is(err, fota.ErrUnsupportedProtocol) {
		t.Fatalf("err %v", err)
	}
	if len(requests(c, 3, "/5/0/1")) != 0 {
		t.Fatal("an unsupported URI was written")
	}

	h.device(testclient.Config{Endpoint: "no-8"}, testclient.FirmwareConfig{Protocols: []int64{}}, "")
	if ps, err := h.mgr.Protocols(h.ctx, "no-8"); err != nil || len(ps) != 1 || ps[0] != fota.CoAP {
		t.Fatalf("protocols %v %v", ps, err)
	}
	if out, err := h.mgr.Run(h.ctx, "no-8", fota.Job{URIs: []string{coap}, Package: img}); err != nil || out.Method != fota.Pull {
		t.Fatalf("%+v %v", out, err)
	}

	h.device(testclient.Config{Endpoint: "only-unknown"}, testclient.FirmwareConfig{Protocols: []int64{77}}, "")
	if ps, err := h.mgr.Protocols(h.ctx, "only-unknown"); err != nil || len(ps) != 1 || ps[0] != fota.CoAP {
		t.Fatalf("protocols %v %v", ps, err)
	}

	h.device(testclient.Config{Endpoint: "push-only"}, testclient.FirmwareConfig{Delivery: 1}, "")
	if out, err := h.mgr.Run(h.ctx, "push-only", fota.Job{URIs: []string{coap}, Package: img}); err != nil || out.Method != fota.Push {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := h.mgr.Run(h.ctx, "push-only", fota.Job{Method: fota.Pull, URIs: []string{coap}}); !errors.Is(err, fota.ErrNoMethod) {
		t.Fatalf("err %v", err)
	}
	c, _ = h.device(testclient.Config{Endpoint: "pull-only"}, testclient.FirmwareConfig{Delivery: 0}, "")
	if _, err := h.mgr.Run(h.ctx, "pull-only", fota.Job{Method: fota.Push, Package: img}); !errors.Is(err, fota.ErrNoMethod) {
		t.Fatalf("err %v", err)
	}
	if _, err := h.mgr.Run(h.ctx, "pull-only", fota.Job{Package: img}); !errors.Is(err, fota.ErrNoMethod) {
		t.Fatalf("err %v", err)
	}
	if len(requests(c, 3, "/5/0/0")) != 0 {
		t.Fatal("pushed to a pull-only client")
	}
	if p, err := fota.ProtocolOf("COAPS+TCP://h/x"); err != nil || p != fota.CoAPTLS {
		t.Fatalf("%v %v", p, err)
	}
}
