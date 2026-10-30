package apiv1

import "testing"

func TestFirmwareUpdateObservedMutation(t *testing.T) {
	s := NewMemoryFirmwareUpdateStore()
	if err := s.Create("a", FirmwareUpdate{ID: "fwu_1", Device: "d", Firmware: "fw"}); err != nil {
		t.Fatal(err)
	}
	obs := FirmwareUpdateObservation{Method: "pull", URI: "coaps://example/fw", DeviceState: "downloading", UpdateResult: &FirmwareUpdateResult{Code: 0, Name: "initial"}, Progress: &FirmwareUpdateProgress{BytesSent: 10, TotalBytes: 100}}
	if s.UpdateObserved("b", "fwu_1", "downloading", obs) {
		t.Fatal("cross-tenant mutation")
	}
	if !s.UpdateObserved("a", "fwu_1", "downloading", obs) {
		t.Fatal("update rejected")
	}
	obs.Progress.BytesSent = 99
	got, _ := s.Get("a", "fwu_1")
	if got.Status != "downloading" || got.Method != "pull" || got.URI != "coaps://example/fw" || got.DeviceState != "downloading" || got.UpdateResult.Name != "initial" || got.Progress.BytesSent != 10 {
		t.Fatalf("observations: %+v", got)
	}
	got.Progress.BytesSent = 88
	if !s.UpdateObserved("a", "fwu_1", "downloading", FirmwareUpdateObservation{Method: "pull", URI: got.URI, DeviceState: "downloading", Progress: &FirmwareUpdateProgress{BytesSent: 20, TotalBytes: 100}}) {
		t.Fatal("same-state progress rejected")
	}
	if s.UpdateObserved("a", "fwu_1", "pending", obs) {
		t.Fatal("invalid transition accepted")
	}
	got, _ = s.Get("a", "fwu_1")
	if got.Status != "downloading" || got.Progress.BytesSent != 20 {
		t.Fatalf("invalid transition changed data: %+v", got)
	}
	outcome := &FirmwareUpdateOutcome{ObjectVersion: "1.1", Added: []string{}, Removed: []string{"/3/0"}}
	if !s.UpdateObserved("a", "fwu_1", "failed", FirmwareUpdateObservation{Method: "pull", URI: got.URI, DeviceState: "idle", Outcome: outcome}) {
		t.Fatal("terminal update rejected")
	}
	outcome.Removed[0] = "changed"
	got, _ = s.Get("a", "fwu_1")
	if got.CompletedAt == nil || got.Outcome.Removed[0] != "/3/0" || got.Outcome.Added == nil {
		t.Fatalf("terminal outcome: %+v", got)
	}
	if s.UpdateObserved("a", "fwu_1", "failed", obs) || s.UpdateObserved("a", "fwu_1", "downloading", obs) {
		t.Fatal("terminal mutation accepted")
	}
}
