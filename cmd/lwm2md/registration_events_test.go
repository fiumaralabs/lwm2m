package main

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestRegistrationEventPayload(t *testing.T) {
	r := &server.Registration{ID: "rd_test", Endpoint: "pump", Version: "1.2", Lifetime: 24 * time.Hour, Binding: "UQ", QueueMode: true,
		SMS: "+3160000", ProfileIDs: []string{"oma:1"}, Objects: []link.Object{{ID: 3, Instances: []uint16{0}}},
		ContentFormats: []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR, lwm2m.FormatTLV},
		Identity:       server.Identity{Mode: server.ModePSK, PSKIdentity: "pump"}, Addr: &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 52114},
		RegisteredAt: time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC), LastUpdate: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	const payload = `{"id":"rd_test","lwm2mVersion":"1.2","lifetimeSeconds":86400,"binding":"UQ","queueMode":true,"sms":"+3160000","profileIds":["oma:1"],"rootPath":"","objects":[{"id":3,"instances":[0]}],"contentFormats":["senml-cbor","lwm2m-cbor","tlv"],"identity":{"mode":"psk","pskIdentity":"pump"},"transport":"U","address":"203.0.113.7:52114","awake":true,"registeredAt":"2026-10-08T06:00:00Z","lastUpdateAt":"2026-10-08T09:00:00Z","expiresAt":"2026-10-09T09:00:00Z"}`
	previous := *r
	previous.SMS, previous.ProfileIDs, previous.ContentFormats = "", nil, nil
	for _, tc := range []struct {
		name  string
		event server.Event
		awake bool
	}{
		{"created", server.Registered{Registration: r}, true},
		{"updated", server.Updated{Registration: r, Previous: previous}, true},
		{"replaced", server.Registered{Registration: r, Replaced: r}, true},
		{"deleted", server.Deregistered{Registration: r, Reason: server.ReasonRemoved}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &memory.Events{}
			registrationEvent(events, tc.event)
			items, _, err := events.List("default", store.EventFilter{Limit: 10})
			if err != nil || len(items) != 1 {
				t.Fatalf("events=%v err=%v", items, err)
			}
			var data map[string]any
			if err := json.Unmarshal(items[0].Data, &data); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			want["awake"] = tc.awake
			if !reflect.DeepEqual(data["registration"], want) {
				t.Errorf("registration=%s; want=%v", items[0].Data, want)
			}
			switch tc.name {
			case "created":
				if data["replaced"] != nil {
					t.Errorf("replaced=%v", data["replaced"])
				}
			case "replaced":
				want["awake"] = false
				if !reflect.DeepEqual(data["replaced"], want) {
					t.Errorf("replaced=%v; want=%v", data["replaced"], want)
				}
			case "updated":
				if !reflect.DeepEqual(data["changed"], []any{"sms", "profileIds", "contentFormats"}) {
					t.Errorf("changed=%v", data["changed"])
				}
			case "deleted":
				if data["reason"] != "removed" {
					t.Errorf("reason=%v", data["reason"])
				}
			}
		})
	}
}
