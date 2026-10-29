package apiv1

import (
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestObservationSeriesTypedTimestamps(t *testing.T) {
	records := &memory.Observations{}
	if err := records.Create("default", store.Observation{Tenant: "default", ID: "id", Device: "pump", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	d := NewObservationDelivery(nil, records, nil, nil, "default")
	d.series = NewMemoryTimeSeriesStore()
	ob := &server.Observation{ID: "token", RegistrationID: "reg"}
	d.active["id"] = ob
	reg := &server.Registration{ID: "reg", Endpoint: "pump"}
	timestamp := time.Unix(1700000000, 250000000).UTC()
	nodes := []lwm2m.Node{
		{Path: lwm2m.MustParsePath("/1/0/1"), Value: lwm2m.Float(12.5), HasTime: true, Time: 1700000000.25},
		{Path: lwm2m.MustParsePath("/1/0/2"), Value: lwm2m.Boolean(true)},
		{Path: lwm2m.MustParsePath("/1/0/3"), Value: lwm2m.String("text")},
		{Path: lwm2m.MustParsePath("/1/0/4"), Value: lwm2m.Opaque([]byte{1})},
		{Path: lwm2m.MustParsePath("/1/0/5"), Value: lwm2m.Objlnk(1, 2)},
	}
	before := time.Now()
	d.HandleEvent(server.Notification{Registration: reg, Observation: ob, Response: &server.Response{Nodes: nodes}})
	after := time.Now()
	for i, want := range []any{12.5, true, nil, nil, nil} {
		points, err := d.series.Query("default", "pump", nodes[i].Path.String(), time.Unix(0, 0), after.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if want == nil {
			if len(points) != 0 {
				t.Fatalf("unexpected points: %v", points)
			}
			continue
		}
		if len(points) != 1 || points[0].V != want {
			t.Fatalf("points: %v", points)
		}
		if i == 0 && !points[0].T.Equal(timestamp) {
			t.Fatalf("device timestamp: %v", points[0].T)
		}
		if i == 1 && (points[0].T.Before(before) || points[0].T.After(after)) {
			t.Fatalf("receive timestamp: %v", points[0].T)
		}
	}
}
