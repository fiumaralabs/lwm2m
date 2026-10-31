package apiv1

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestBootstrapSessionRecorderRedactsAndPreservesStepOrder(t *testing.T) {
	store := NewMemoryBootstrapSessionStore()
	record := NewBootstrapSessionRecorder(store, "tenant")
	now := time.Now().UTC()
	f := lwm2m.FormatTLV
	for _, fail := range []bool{false, true} {
		r := bootstrap.Result{Endpoint: "device", StartedAt: now, EndedAt: now.Add(time.Second), Format: f,
			Query: []string{"secret=private-key"}, Steps: []bootstrap.Step{{Code: codes.PUT, Path: lwm2m.MustParsePath("/0/1"), Format: &f, Result: codes.Changed}, {Code: codes.POST, Path: lwm2m.Root, Result: codes.Changed}}}
		if fail {
			r.Err = errors.New("PSK-super-secret private-key")
		}
		if err := record(r); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := store.ListForDevice("tenant", "device", "", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("records=%+v err=%v", got, err)
	}
	if got[0].Status != "failed" || got[1].Status != "succeeded" || got[0].ID == got[1].ID || got[0].ID == "" {
		t.Fatalf("outcomes: %+v", got)
	}
	for _, r := range got {
		if len(r.Steps) != 2 || r.Steps[0].Method != "PUT" || r.Steps[0].Path != "/0/1" || r.Steps[0].Code != "2.04" || r.Steps[1].Path != "/bs" {
			t.Fatalf("steps: %+v", r.Steps)
		}
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "secret") || strings.Contains(string(data), "private-key") {
			t.Fatalf("secret leaked: %s", data)
		}
	}
}
