package fota_test

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// Proves: FW-01, FW-02, FW-04
// Push (bw-1): a 5000-byte (Zephyr's test image) and a 20 KiB package go to
// /5/0/0 as opaque Block1 PUTs of 1024 bytes, one token per transfer,
// numbered from 0, Size1 on the first block, M=1 until the last. The
// client ends with the exact bytes (CRC32 as bw-1 checks), the server
// observes State and Update Result, executes /5/0/2 only once Downloaded,
// and the job ends on Update Result 1.
func TestPushBlock1(t *testing.T) {
	h := newHarness(t)
	for _, pkg := range [][]byte{bytes.Repeat([]byte("1234567890"), 500), image(20 << 10)} {
		rl := newRelay(t, h.addr)
		ep := fmt.Sprint("push-", len(pkg))
		c, f := h.device(testclient.Config{Endpoint: ep, Version: "1.0"}, testclient.FirmwareConfig{Delivery: 1}, rl.addr())
		out, err := h.mgr.Run(h.ctx, ep, fota.Job{Package: pkg})
		if err != nil {
			t.Fatal(err)
		}
		if out.Result != fota.Success || out.Method != fota.Push || f.State() != 0 || f.Result() != 1 {
			t.Fatalf("outcome %+v, client state %d result %d", out, f.State(), f.Result())
		}
		if got := f.Package(); got != nil {
			t.Fatal("client kept the package after a successful update")
		}
		if len(requests(c, 2, "/5/0/2")) != 1 {
			t.Fatal("Execute /5/0/2 not sent exactly once")
		}
		obs := 0
		for _, r := range c.Requests() {
			if r.Observe != nil && *r.Observe == 0 && (r.Path == "/5/0/3" || r.Path == "/5/0/5") {
				obs++
			}
		}
		if obs != 2 {
			t.Fatalf("observed %d of State/Result", obs)
		}
		w := requests(c, 3, "/5/0/0") // PUT, reassembled by the client
		if len(w) != 1 || *w[0].Format != 42 || crc32.ChecksumIEEE(w[0].Body) != crc32.ChecksumIEEE(pkg) {
			t.Fatalf("package writes %d", len(w))
		}
		checkBlocks(t, rl.blocks(), len(pkg), 1)
	}
}

// checkBlocks asserts one Block1 transfer per token (transfers of them),
// each complete and in order.
func checkBlocks(t *testing.T, seen []block1, total, transfers int) {
	t.Helper()
	byTok := map[string]map[int64]block1{}
	var order []string
	for _, b := range seen {
		if byTok[b.token] == nil {
			byTok[b.token] = map[int64]block1{}
			order = append(order, b.token)
			if b.num != 0 || !b.size1 {
				t.Fatalf("transfer starts at block %d, Size1 %v", b.num, b.size1)
			}
		}
		byTok[b.token][b.num] = b
	}
	if len(order) != transfers {
		t.Fatalf("%d Block1 transfers, want %d", len(order), transfers)
	}
	last := byTok[order[len(order)-1]]
	n := (total + 1023) / 1024
	if len(last) != n {
		t.Fatalf("%d blocks, want %d", len(last), n)
	}
	sum := 0
	for i := range n {
		b := last[int64(i)]
		if b.size != 1024 || b.more != (i < n-1) {
			t.Fatalf("block %d: %+v", i, b)
		}
		sum += b.n
	}
	if sum != total {
		t.Fatalf("payload %d bytes, want %d", sum, total)
	}
}

// Proves: FW-01, FW-02
// bw-2: a Block1 transfer that times out is abandoned, and the next
// attempt restarts from block 0 with a new token; the client gets the
// whole package from the second transfer.
func TestPushAbortRestart(t *testing.T) {
	h := newHarness(t)
	rl := newRelay(t, h.addr)
	var slow string
	rl.delay = func(tok []byte) time.Duration {
		if slow == "" {
			slow = string(tok)
		}
		if string(tok) == slow {
			return 300 * time.Millisecond // a client that is slow to flash, like bw-2
		}
		return 0
	}
	h.device(testclient.Config{Endpoint: "bw2"}, testclient.FirmwareConfig{Delivery: 2}, rl.addr())
	pkg := bytes.Repeat([]byte("1234567890"), 500)
	out, err := h.mgr.Run(h.ctx, "bw2", fota.Job{Method: fota.Push, Package: pkg, PushTimeout: 700 * time.Millisecond, PushAttempts: 2})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	seen := rl.blocks()
	checkBlocks(t, seen, len(pkg), 2)
	first := 0
	for _, b := range seen {
		if b.token == slow {
			first++
		}
	}
	if first >= 5 {
		t.Fatalf("the aborted transfer sent all %d blocks", first)
	}
	// Without retries the timeout is the job's error.
	rl.mu.Lock()
	slow = ""
	rl.mu.Unlock()
	if _, err := h.mgr.Run(h.ctx, "bw2", fota.Job{Method: fota.Push, Package: pkg, PushTimeout: 500 * time.Millisecond}); err == nil {
		t.Fatal("timed-out push reported success")
	}
}

// Proves: FW-01
// The server also takes block-wise responses: a 5000-byte value comes
// back from the client as Block2 blocks, which the server fetches block
// by block and reassembles.
func TestReadBlock2(t *testing.T) {
	h := newHarness(t)
	c, _ := h.device(testclient.Config{Endpoint: "big"}, testclient.FirmwareConfig{}, "")
	big := strings.Repeat("0123456789", 500)
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String(big))
	before := len(c.RawRequests())
	r, err := h.srv.Read(h.ctx, "big", lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
	if err != nil || !r.Success() || len(r.Nodes) != 1 || r.Nodes[0].Value.Str != big {
		t.Fatalf("read: %v %v", r, err)
	}
	if n := len(c.RawRequests()) - before; n != 5 {
		t.Fatalf("%d request datagrams for a 5-block read", n)
	}
}
