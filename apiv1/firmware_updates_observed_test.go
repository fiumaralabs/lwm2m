package apiv1

import (
	"encoding/json"
	"testing"
)

func TestFirmwareUpdateEmptyOutcomeArrays(t *testing.T) {
	s := NewMemoryFirmwareUpdateStore()
	u := FirmwareUpdate{ID: "fwu_empty", Device: "device", Firmware: "fw", Outcome: &FirmwareUpdateOutcome{Added: []string{}, Removed: []string{}}}
	if err := s.Create("tenant", u); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("tenant", u.ID)
	b, err := json.Marshal(got.Outcome)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"objectVersion":"","added":[],"removed":[]}` {
		t.Fatalf("unexpected outcome: %s", b)
	}
	var round FirmwareUpdateOutcome
	if err := json.Unmarshal(b, &round); err != nil || round.Added == nil || round.Removed == nil {
		t.Fatalf("round trip: %+v %v", round, err)
	}
}
