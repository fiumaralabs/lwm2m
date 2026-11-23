package storetest

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

type deviceReplay interface {
	ReplayDeviceCreate(context.Context, string, string, func() (int, http.Header, []byte)) (int, http.Header, []byte, error)
}

// DeviceReplay tests replay against real device and event stores.
func DeviceReplay(t *testing.T, devices store.Devices, events store.Events) {
	t.Helper()
	replay, ok := devices.(deviceReplay)
	if !ok {
		t.Fatal("device store does not support create replay")
	}
	run := func(tenant, endpoint string) func() (int, http.Header, []byte) {
		return func() (int, http.Header, []byte) {
			if _, err := devices.Create(tenant, store.Device{Endpoint: endpoint}); err != nil {
				return 500, nil, []byte(err.Error())
			}
			if _, err := events.Append(tenant, "device.created", endpoint, []byte(`{}`)); err != nil {
				return 500, nil, []byte(err.Error())
			}
			return 201, http.Header{"Location": {"/v1/devices/" + endpoint}, "X-Multi": {"a", "b"}}, []byte(endpoint)
		}
	}
	call := func(tenant, key, endpoint string) (int, http.Header, []byte, error) {
		return replay.ReplayDeviceCreate(context.Background(), tenant, key, run(tenant, endpoint))
	}
	status, header, body, err := call("a", "key", "first")
	if err != nil || status != 201 {
		t.Fatalf("first: %d %s %v", status, body, err)
	}
	for _, endpoint := range []string{"first", "changed"} {
		s, h, b, e := call("a", "key", endpoint)
		if e != nil || s != status || !reflect.DeepEqual(h, header) || string(b) != string(body) {
			t.Fatalf("retry: %d %v %s %v", s, h, b, e)
		}
	}
	s, _, b, err := call("b", "key", "other")
	if err != nil || s != 201 || string(b) != "other" {
		t.Fatalf("tenant: %d %s %v", s, b, err)
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _, b, err := call("c", "concurrent", "same")
			if err != nil || s != 201 || string(b) != "same" {
				errs <- fmt.Errorf("%d %s %v", s, b, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for _, tenant := range []string{"a", "b", "c"} {
		ds, _, err := devices.List(tenant, store.DeviceFilter{}, "", 100)
		if err != nil || len(ds) != 1 {
			t.Errorf("%s devices: %v %v", tenant, ds, err)
		}
		es, _, err := events.List(tenant, store.EventFilter{Limit: 100})
		if err != nil || len(es) != 1 {
			t.Errorf("%s events: %v %v", tenant, es, err)
		}
	}
}
