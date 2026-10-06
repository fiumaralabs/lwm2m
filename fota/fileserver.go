package fota

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
)

// FileServer is a firmware repository for Package URI pulls (FW-02): a
// CoAP/CoAPs server with stateless Block2 (RFC 7959 §2.4) and an
// http.Handler for HTTP(S) URIs.
//
// Block2 is served per request from the requested block number, so it
// works whether a client reuses one token for every block (Zephyr) or
// takes a new one per block, and it never looks at the Uri-Query (Zephyr
// drops it). Every block carries Size2 and an ETag of the image.
type FileServer struct {
	// MaxSZX caps the block size: 6 (1024 bytes) by default. A client asking
	// for smaller blocks gets them (RFC 7959 §2.4).
	MaxSZX blockwise.SZX

	mu      sync.Mutex
	files   map[string]file
	stops   []func()
	closers []func() error
}

type file struct {
	data []byte
	etag []byte
	mod  time.Time
}

// NewFileServer returns an empty repository.
func NewFileServer() *FileServer {
	return &FileServer{MaxSZX: blockwise.SZX1024, files: map[string]file{}}
}

// Add publishes data at path ("/fw/app-1.2.bin"). The URI given to a
// client is scheme://host:port + path, with no query.
func (f *FileServer) Add(path string, data []byte) {
	sum := sha256.Sum256(data)
	f.mu.Lock()
	f.files["/"+strings.Trim(path, "/")] = file{data: data, etag: sum[:8], mod: time.Now()}
	f.mu.Unlock()
}

// Remove unpublishes path.
func (f *FileServer) Remove(path string) {
	f.mu.Lock()
	delete(f.files, "/"+strings.Trim(path, "/"))
	f.mu.Unlock()
}

func (f *FileServer) get(path string) (file, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.files["/"+strings.Trim(path, "/")]
	return d, ok
}

// ListenUDP serves coap:// on addr.
func (f *FileServer) ListenUDP(addr string) (net.Addr, error) {
	l, err := coapnet.NewListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	srv := udp.NewServer(options.WithMux(f.router()), options.WithBlockwise(false, blockwise.SZX1024, time.Minute))
	f.track(srv.Stop, l.Close)
	go func() { _ = srv.Serve(l) }()
	return l.LocalAddr(), nil
}

// ListenDTLS serves coaps:// on addr with the given DTLS configuration
// (PSK, RPK or certificates).
func (f *FileServer) ListenDTLS(addr string, cfg *piondtls.Config) (net.Addr, error) {
	l, err := coapnet.NewDTLSListener("udp", addr, cfg)
	if err != nil {
		return nil, err
	}
	srv := coapdtls.NewServer(options.WithMux(f.router()), options.WithBlockwise(false, blockwise.SZX1024, time.Minute))
	f.track(srv.Stop, l.Close)
	go func() { _ = srv.Serve(l) }()
	return l.Addr(), nil
}

// router routes every request to serveCoAP. go-coap's own blockwise stays
// off: it caches responses by token, which a client taking a new token per
// block would miss.
func (f *FileServer) router() *mux.Router {
	r := mux.NewRouter()
	r.DefaultHandle(mux.HandlerFunc(f.serveCoAP))
	return r
}

func (f *FileServer) track(stop func(), closer func() error) {
	f.mu.Lock()
	f.stops = append(f.stops, stop)
	f.closers = append(f.closers, closer)
	f.mu.Unlock()
}

// Close stops all listeners.
func (f *FileServer) Close() error {
	f.mu.Lock()
	stops, closers := f.stops, f.closers
	f.stops, f.closers = nil, nil
	f.mu.Unlock()
	for _, s := range stops {
		s()
	}
	var errs []error
	for _, c := range closers {
		if err := c(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *FileServer) serveCoAP(w mux.ResponseWriter, m *mux.Message) {
	if m.Code() != codes.GET {
		_ = w.SetResponse(codes.MethodNotAllowed, message.TextPlain, nil)
		return
	}
	path, _ := m.Options().Path()
	img, ok := f.get(path)
	if !ok {
		_ = w.SetResponse(codes.NotFound, message.TextPlain, nil)
		return
	}
	szx, num := f.MaxSZX, int64(0)
	if v, err := m.Options().GetUint32(message.Block2); err == nil {
		s, n, _, err := blockwise.DecodeBlockOption(v)
		if err != nil || s == blockwise.SZXBERT {
			_ = w.SetResponse(codes.BadOption, message.TextPlain, nil)
			return
		}
		szx, num = min(s, f.MaxSZX), n
	}
	size := szx.Size()
	off := num * size
	total := int64(len(img.data))
	if off > total || (off == total && total > 0) {
		_ = w.SetResponse(codes.BadOption, message.TextPlain, nil) // block beyond the end
		return
	}
	end := min(off+size, total)
	_ = w.SetResponse(codes.Content, message.AppOctets, bytes.NewReader(img.data[off:end]))
	resp := w.Message()
	resp.SetOptionBytes(message.ETag, img.etag)
	resp.SetOptionUint32(message.Size2, uint32(total))
	if total > size || num > 0 {
		v, _ := blockwise.EncodeBlockOption(szx, num, end < total)
		resp.SetOptionUint32(message.Block2, v)
	}
}

// ServeHTTP serves http:// and https:// Package URIs (FW-02: HTTP(S) MAY
// be used); mount it on an http.Server or httptest.Server.
func (f *FileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	img, ok := f.get(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+hex.EncodeToString(img.etag)+`"`)
	http.ServeContent(w, r, "", img.mod, bytes.NewReader(img.data))
}
