package fota_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

var temp = lwm2m.MustParsePath("/3303/0/5700")

// Proves: FW-05
// After an update the client drops an object the new firmware no longer
// supports, adds one, and re-registers. The server follows the job onto
// the new registration (its observations were voided, so it observes
// again; polling is off), and the outcome carries the new registration
// and the added and removed objects and instances. An Update that shrinks
// the list makes the server forget observations of the removed objects.
func TestReconcileAfterUpdate(t *testing.T) {
	h := newHarness(t)
	c := testclient.New(testclient.Config{Endpoint: "reboot"})
	c.Set(temp, lwm2m.Float(21.5))
	var f *testclient.Firmware
	f = testclient.NewFirmware(c, testclient.FirmwareConfig{Version: "1.1", Delivery: 2,
		Install: func([]byte) int64 {
			c.RemoveObject(3303)
			c.Set(lwm2m.MustParsePath("/3304/0/5700"), lwm2m.Float(40))
			if _, err := f.Register(context.Background()); err != nil {
				t.Error(err)
			}
			return 1
		}})
	if err := c.Dial(h.addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := f.Register(h.ctx); err != nil {
		t.Fatal(err)
	}
	out, err := h.mgr.Run(h.ctx, "reboot", fota.Job{Package: image(4097), Poll: time.Minute})
	if err != nil || out.Result != fota.Success {
		t.Fatalf("%+v %v", out, err)
	}
	if out.After == out.Before || out.After.ID == out.Before.ID {
		t.Fatal("outcome still has the old registration")
	}
	want := func(got []lwm2m.Path, ps ...string) {
		t.Helper()
		var w []lwm2m.Path
		for _, p := range ps {
			w = append(w, lwm2m.MustParsePath(p))
		}
		if !slices.Equal(got, w) {
			t.Fatalf("got %v, want %v", got, w)
		}
	}
	want(out.Added, "/3304", "/3304/0")
	want(out.Removed, "/3303", "/3303/0")

	// Update with a shrunk list: observations of the removed object go.
	c.Set(temp, lwm2m.Float(22))
	if _, err := f.Register(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, r, err := h.srv.Observe(h.ctx, "reboot", temp, server.ObserveOptions{}); err != nil || !r.Success() {
		t.Fatalf("observe: %v %v", r, err)
	}
	if _, r, err := h.srv.Observe(h.ctx, "reboot", lwm2m.MustParsePath("/3304/0/5700"), server.ObserveOptions{}); err != nil || !r.Success() {
		t.Fatalf("observe: %v %v", r, err)
	}
	c.RemoveObject(3303)
	if r, err := c.Update(h.ctx, nil, []byte(f.Links())); err != nil || r.Code != 68 {
		t.Fatalf("update: %v %v", r, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		obs := h.srv.Observations("reboot")
		if len(obs) == 1 && obs[0].Paths[0].Object() == 3304 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("observations %d", len(obs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Proves: OBJ-01
// The server supports the mandatory Security, Server and Device objects
// and the recommended Access Control, Connectivity Monitoring, Firmware
// Update, Location and Connectivity Statistics objects: each has a model
// in the built-in registry, and /5 has v1.0, v1.1 and v1.2 with the
// resources fota drives, resolved per registration.
func TestObjectSupport(t *testing.T) {
	reg := model.Default()
	for _, id := range []uint16{0, 1, 3, 2, 4, 5, 6, 7} {
		if len(reg.Versions(id)) == 0 {
			t.Fatalf("no model for /%d", id)
		}
	}
	res := map[string][]uint16{"1.0": {0, 1, 2, 3, 5, 6, 7, 8, 9}, "1.1": {10, 11, 12, 13}, "1.2": {14}}
	var all []uint16
	for _, v := range []string{"1.0", "1.1", "1.2"} {
		all = append(all, res[v]...)
		ver, _ := model.ParseVersion(v)
		o, ok := reg.Get(5, ver)
		if !ok || o.Version != ver {
			t.Fatalf("/5 v%s missing", v)
		}
		for _, id := range all {
			if _, ok := o.Resource(id); !ok {
				t.Fatalf("/5 v%s lacks resource %d", v, id)
			}
		}
	}
	for lv, want := range map[string]string{"1.0": "1.0", "1.1": "1.0", "1.2": "1.1"} {
		v, err := fota.Version(&server.Registration{Version: lv, Objects: []link.Object{{ID: 5}}})
		if err != nil || v.String() != want {
			t.Fatalf("lwm2m=%s: /5 v%v %v, want %s", lv, v, err, want)
		}
	}
	if v, _ := fota.Version(&server.Registration{Version: "1.1", Objects: []link.Object{{ID: 5, Version: "1.2"}}}); v.String() != "1.2" {
		t.Fatalf("ver=1.2 resolved as %v", v)
	}
	if _, err := fota.Version(&server.Registration{Version: "1.1"}); err != fota.ErrNoFirmwareObject {
		t.Fatalf("no /5: %v", err)
	}
}
