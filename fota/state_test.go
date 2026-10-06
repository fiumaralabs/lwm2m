package fota_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func resultErr(t *testing.T, err error, want fota.Result) {
	t.Helper()
	var re *fota.ResultError
	if !errors.As(err, &re) || re.Result != want {
		t.Fatalf("err %v, want result %v", err, want)
	}
}

// Proves: FW-04
// Errors come only through Update Result: an integrity failure (5) after
// download, an invalid URI (7) and a failed install (8) end the job with
// that result. The next job first resets the state machine by writing an
// empty Package URI (or a NUL Package for push), so the old result is not
// taken for the new one, and then succeeds. Execute is sent only in
// Downloaded; a client in Updating makes Run refuse.
func TestFailuresAndReset(t *testing.T) {
	h := newHarness(t)
	img := image(3000)
	h.files.Add("/fw/ok.bin", img)
	h.files.Add("/fw/bad.bin", image(3001))
	install := int64(8)
	c, f := h.device(testclient.Config{Endpoint: "fail"}, testclient.FirmwareConfig{Delivery: 2, Verify: verifyEqual(img),
		Install: func([]byte) int64 { return install }}, "")
	base := "coap://" + h.coap

	_, err := h.mgr.Run(h.ctx, "fail", fota.Job{URIs: []string{base + "/fw/bad.bin"}})
	resultErr(t, err, fota.IntegrityFailure)
	if f.State() != 0 || len(requests(c, 2, "/5/0/2")) != 0 {
		t.Fatalf("state %d after integrity failure", f.State())
	}

	_, err = h.mgr.Run(h.ctx, "fail", fota.Job{URIs: []string{"coap:///no-host"}})
	resultErr(t, err, fota.InvalidURI)
	uris := requests(c, 3, "/5/0/1")
	if len(uris) != 3 || string(uris[1].Body) != "" {
		t.Fatalf("URI writes %d", len(uris))
	}

	_, err = h.mgr.Run(h.ctx, "fail", fota.Job{URIs: []string{base + "/fw/ok.bin"}})
	resultErr(t, err, fota.UpdateFailed)
	if f.State() != 2 {
		t.Fatalf("state %d after failed install", f.State())
	}

	install = 1
	out, err := h.mgr.Run(h.ctx, "fail", fota.Job{Method: fota.Push, Package: img})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	pk := requests(c, 3, "/5/0/0")
	if len(pk) != 2 || string(pk[0].Body) != "\x00" {
		t.Fatalf("package writes %d", len(pk))
	}

	// Manual resets: NUL Package, empty URI.
	if err := h.mgr.Reset(h.ctx, "fail", true); err != nil || f.State() != 0 || f.Result() != 0 {
		t.Fatalf("reset: %v %d %d", err, f.State(), f.Result())
	}
	if err := h.mgr.Reset(h.ctx, "fail", false); err != nil || f.State() != 0 {
		t.Fatalf("reset: %v", err)
	}
	c.Set(lwm2m.MustParsePath("/5/0/3"), lwm2m.Integer(3))
	if _, err := h.mgr.Run(h.ctx, "fail", fota.Job{Package: img}); !errors.Is(err, fota.ErrBusy) {
		t.Fatalf("err %v", err)
	}
	if r, err := h.srv.Execute(h.ctx, "fail", fota.PathUpdate, ""); err != nil || r.Code != codes.MethodNotAllowed {
		t.Fatalf("execute in Updating: %v %v", r, err)
	}
}

// Proves: FW-04, FW-06
// /5 v1.1: the server writes Severity and Maximum Defer Period before
// delivery. A user deferral (Update Result 11, State stays Downloaded)
// ends Run with Deferred and no error; Resume executes the downloaded
// package without downloading again. A critical package is not deferred.
// Cancel (/5/0/10) moves to Idle with result 10 and ends a running job;
// during Updating the client answers 4.05. On a v1.0 object, Cancel and
// v1.1 resources are refused before anything is sent.
func TestDeferSeverityCancel(t *testing.T) {
	h := newHarness(t)
	img := image(4500)
	c, f := h.device(testclient.Config{Endpoint: "v11"}, testclient.FirmwareConfig{Version: "1.1", Delivery: 2, Defer: true}, "")
	out, err := h.mgr.Run(h.ctx, "v11", fota.Job{Package: img, Severity: ptr(int64(2)), MaxDeferPeriod: ptr(uint64(3600))})
	if err != nil || out.Result != fota.Deferred || f.State() != 2 || out.Version.String() != "1.1" {
		t.Fatalf("%+v %v state %d", out, err, f.State())
	}
	if v, _ := c.Get(lwm2m.MustParsePath("/5/0/11")); v.Int != 2 {
		t.Fatalf("severity %v", v)
	}
	if v, _ := c.Get(lwm2m.MustParsePath("/5/0/13")); v.Uint != 3600 {
		t.Fatalf("max defer %v", v)
	}
	f.SetDefer(false)
	out, err = h.mgr.Run(h.ctx, "v11", fota.Job{Resume: true, Poll: 50 * time.Millisecond})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	if len(requests(c, 3, "/5/0/0")) != 1 {
		t.Fatal("resume downloaded again")
	}

	// Critical severity: no deferral even though the user would.
	f.SetDefer(true)
	out, err = h.mgr.Run(h.ctx, "v11", fota.Job{Package: img, Severity: ptr(int64(0)), MaxDeferPeriod: ptr(uint64(3600))})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("critical: %+v %v", out, err)
	}

	// Cancel a deferred update.
	f.SetDefer(true)
	if out, err := h.mgr.Run(h.ctx, "v11", fota.Job{Package: img, Severity: ptr(int64(1))}); err != nil || out.Result != fota.Deferred {
		t.Fatalf("%+v %v", out, err)
	}
	if err := h.mgr.Cancel(h.ctx, "v11"); err != nil || f.State() != 0 || f.Result() != 10 {
		t.Fatalf("cancel: %v %d %d", err, f.State(), f.Result())
	}

	// Cancel during a running download ends the job with result 10.
	gate := make(chan struct{})
	_, g := h.device(testclient.Config{Endpoint: "v11-pull"}, testclient.FirmwareConfig{Version: "1.1", Delivery: 0,
		Verify: func([]byte) int64 { <-gate; return 0 }}, "")
	h.files.Add("/fw/c.bin", img)
	done := make(chan error, 1)
	go func() {
		_, err := h.mgr.Run(h.ctx, "v11-pull", fota.Job{URIs: []string{"coap://" + h.coap + "/fw/c.bin"}})
		done <- err
	}()
	for g.State() != 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.mgr.Cancel(h.ctx, "v11-pull"); err != nil {
		t.Fatal(err)
	}
	resultErr(t, <-done, fota.Cancelled)
	close(gate)

	// Cancel while Updating: 4.05.
	c.Set(lwm2m.MustParsePath("/5/0/3"), lwm2m.Integer(3))
	if err := h.mgr.Cancel(h.ctx, "v11"); !errors.Is(err, fota.ErrRejected) {
		t.Fatalf("cancel while updating: %v", err)
	}

	c10, _ := h.device(testclient.Config{Endpoint: "v10"}, testclient.FirmwareConfig{Delivery: 2}, "")
	if err := h.mgr.Cancel(h.ctx, "v10"); !errors.Is(err, fota.ErrVersion) {
		t.Fatalf("v1.0 cancel: %v", err)
	}
	if _, err := h.mgr.Run(h.ctx, "v10", fota.Job{Package: img, Severity: ptr(int64(0))}); !errors.Is(err, fota.ErrVersion) {
		t.Fatalf("v1.0 severity: %v", err)
	}
	if len(c10.Requests()) != 0 {
		t.Fatal("requests sent to a v1.0 object for v1.1 features")
	}
}

// Proves: FW-06
// /5 v1.2 Automatic Upgrade at Download: with true the server writes
// /5/0/14 and never executes /5/0/2; the client installs once the
// download completes. With false the server executes. A v1.1 object
// (the default for a 1.2 client without "ver") is refused the resource.
func TestAutoUpgrade(t *testing.T) {
	h := newHarness(t)
	img := image(6000)
	h.files.Add("/fw/a.bin", img)
	c, _ := h.device(testclient.Config{Endpoint: "v12", Version: "1.2"}, testclient.FirmwareConfig{Version: "1.2", Delivery: 2}, "")
	out, err := h.mgr.Run(h.ctx, "v12", fota.Job{URIs: []string{"coap://" + h.coap + "/fw/a.bin"}, AutoUpgrade: ptr(true)})
	if err != nil || out.Result != fota.Success || out.Version.String() != "1.2" {
		t.Fatalf("%+v %v", out, err)
	}
	if v, _ := c.Get(lwm2m.MustParsePath("/5/0/14")); !v.Bool {
		t.Fatal("/5/0/14 not written")
	}
	if len(requests(c, 2, "/5/0/2")) != 0 {
		t.Fatal("Execute sent despite automatic upgrade")
	}
	if out, err := h.mgr.Run(h.ctx, "v12", fota.Job{Package: img, AutoUpgrade: ptr(false)}); err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	if len(requests(c, 2, "/5/0/2")) != 1 {
		t.Fatal("no Execute with automatic upgrade off")
	}

	cl := testclient.New(testclient.Config{Endpoint: "v12-default", Version: "1.2"})
	testclient.NewFirmware(cl, testclient.FirmwareConfig{Version: "1.1"})
	if err := cl.Dial(h.addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if r, err := cl.Register(h.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("%v %v", r, err)
	}
	reg, _ := h.srv.Store().ByEndpoint("v12-default")
	if v, err := fota.Version(reg); err != nil || v.String() != "1.1" {
		t.Fatalf("version %v %v", v, err)
	}
	if _, err := h.mgr.Run(h.ctx, "v12-default", fota.Job{Package: img, AutoUpgrade: ptr(true)}); !errors.Is(err, fota.ErrVersion) {
		t.Fatalf("err %v", err)
	}
}

// Proves: FW-04
// A client that refuses Observe on /5 is tracked by polling State and
// Update Result.
func TestPollingFallback(t *testing.T) {
	h := newHarness(t)
	c, f := h.device(testclient.Config{Endpoint: "noobs"}, testclient.FirmwareConfig{Delivery: 2}, "")
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Observe != nil {
			return codes.MethodNotAllowed, nil, nil, true
		}
		return f.Handle(r)
	})
	out, err := h.mgr.Run(h.ctx, "noobs", fota.Job{Package: image(1500), Poll: 20 * time.Millisecond})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	if len(h.srv.Observations("noobs")) != 0 {
		t.Fatal("observation left behind")
	}
}
