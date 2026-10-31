package apiv1

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"net"
	"strings"
	"testing"
	"time"
)

type recorderPeer struct{ fail bool }

func (p recorderPeer) Identity() server.Identity { return server.Identity{Mode: server.ModeNoSec} }
func (p recorderPeer) RemoteAddr() net.Addr      { return nil }
func (p recorderPeer) Binding() string           { return "U" }
func (p recorderPeer) Exchange(_ context.Context, m *server.Message) (*server.Message, error) {
	if p.fail && m.Code == codes.PUT {
		return nil, errors.New("PSK-super-secret private-key")
	}
	if m.Code == codes.DELETE {
		return &server.Message{Code: codes.Deleted}, nil
	}
	return &server.Message{Code: codes.Changed}, nil
}

func TestBootstrapServerRecordsIntoStore(t *testing.T) {
	configs := bootstrap.NewMemoryConfigStore()
	id := uint16(1)
	if err := configs.Put("device", &bootstrap.BootstrapConfig{
		Security: map[uint16]bootstrap.SecurityConfig{0: {URI: "coaps://example.com", SecurityMode: bootstrap.ModePSK, PublicKeyOrID: bootstrap.Bytes("identity"), SecretKey: bootstrap.Bytes("PSK-super-secret"), ServerID: &id}},
		Servers:  map[uint16]bootstrap.ServerConfig{0: {ShortID: 1, Lifetime: 60, Binding: "U"}},
	}); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryBootstrapSessionStore()
	done := make(chan bootstrap.Result, 2)
	s := bootstrap.New(bootstrap.Config{Configs: configs, Recorder: NewBootstrapSessionRecorder(store, "tenant"), OnSession: func(r bootstrap.Result) { done <- r }})
	defer s.Close()
	for _, fail := range []bool{false, true} {
		resp, after := s.HandleUplink(recorderPeer{fail: fail}, &server.Message{Code: codes.POST, Path: "/bs", Query: []string{"ep=device"}})
		if resp.Code != codes.Changed {
			t.Fatalf("uplink: %v", resp.Code)
		}
		after()
		select {
		case r := <-done:
			if (r.Err != nil) != fail {
				t.Fatalf("result: %v", r.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("session timed out")
		}
	}
	records, _, err := store.ListForDevice("tenant", "device", "", 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if records[0].Status != "failed" || records[1].Status != "succeeded" {
		t.Fatalf("outcomes: %+v", records)
	}
	if len(records[0].Steps) == 0 || len(records[1].Steps) < 2 || records[1].Steps[len(records[1].Steps)-1].Path != "/bs" {
		t.Fatalf("steps: %+v", records)
	}
	b, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "PSK-super-secret") || strings.Contains(string(b), "private-key") {
		t.Fatal("secret leaked")
	}
}
