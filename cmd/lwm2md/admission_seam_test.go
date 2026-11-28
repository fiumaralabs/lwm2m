package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestAdmissionHookUnavailableRejectsNewRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.db")
	hook, err := buildAdmissionHook(admissionSettings{URL: "https://example.com/v1/admission-decisions", Timeout: time.Second, RequestKeyRef: "UNSET_REQUEST_KEY", ResponseKeyRef: "UNSET_RESPONSE_KEY"}, "sqlite:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer hook.Close()
	devices, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer devices.Close()
	api := leshanapi.New(leshanapi.Options{Prefix: "/api"})
	registered := make(chan struct{}, 1)
	srv, _, executor, dispatcher := newDaemonWithAdmission(server.Config{OnEvent: func(e server.Event) {
		if _, ok := e.(server.Registered); ok {
			registered <- struct{}{}
		}
	}}, api, apiv1.Keys{}, true, apiv1.NewMemoryBootstrapSessionStore(), nil, hook, devices)
	defer srv.Close()
	defer executor.Close()
	defer dispatcher.Close()
	cb := coap.New(srv)
	defer cb.Close()
	key := []byte("0123456789abcdef")
	if err := srv.Security().Put(server.SecurityInfo{Endpoint: "admission-seam-test", PSKIdentity: "seam-identity", PSKKey: key}); err != nil {
		t.Fatal(err)
	}
	addr, err := cb.ListenDTLS("127.0.0.1:0", coap.DTLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "admission-seam-test"})
	defer c.Close()
	if err := c.DialDTLS(addr.String(), testclient.PSKConfig("seam-identity", key)...); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.Register(ctx)
	if err != nil || res.Code != codes.ServiceUnavailable {
		t.Fatalf("register = %+v, %v; want service unavailable", res, err)
	}
	select {
	case <-registered:
		t.Fatal("rejected registration emitted an event")
	default:
	}
}
