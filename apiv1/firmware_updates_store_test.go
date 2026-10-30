package apiv1

import (
	"errors"
	"testing"
	"time"
)

func TestFirmwareUpdateStore(t *testing.T) {
	s := NewMemoryFirmwareUpdateStore()
	u := FirmwareUpdate{ID: "fwu_1", Device: "d", Firmware: "fw_1", CreatedAt: time.Now()}
	if err := s.Create("a", u); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("a", u.ID)
	if !ok || got.Status != "pending" || got.Method != "auto" || got.PushTimeoutSeconds != 300 || got.PushAttempts != 1 || got.PollSeconds != 30 {
		t.Fatalf("defaults: %+v", got)
	}
	if _, ok := s.Get("b", u.ID); ok {
		t.Fatal("tenant leak")
	}
	u.ID = "fwu_2"
	if err := s.Create("a", u); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("active: %v", err)
	}
	if err := s.Create("b", u); err != nil {
		t.Fatal(err)
	}
	if s.UpdateStatus("b", "fwu_1", "downloading") {
		t.Fatal("tenant leak")
	}
	for _, status := range []string{"downloading", "downloaded", "updating", "succeeded"} {
		if !s.UpdateStatus("a", "fwu_1", status) {
			t.Fatal(status)
		}
	}
	if s.UpdateStatus("a", "fwu_1", "failed") {
		t.Fatal("terminal transition")
	}
	u.CreatedAt = u.CreatedAt.Add(time.Second)
	if err := s.Create("a", u); err != nil {
		t.Fatal(err)
	}
	list := s.ListForDevice("a", "d")
	if len(list) != 2 || list[0].ID != "fwu_2" || list[1].ID != "fwu_1" || list[1].CompletedAt == nil {
		t.Fatalf("list: %+v", list)
	}
}

func TestFirmwareUpdateValidation(t *testing.T) {
	for _, u := range []FirmwareUpdate{{}, {Firmware: "a", URI: "b"}, {Firmware: "a", Method: "bogus"}, {Firmware: "a", Severity: "bogus"}, {Firmware: "a", PushAttempts: -1}} {
		if ValidateFirmwareUpdate(&u) == nil {
			t.Fatalf("accepted %+v", u)
		}
	}
	u := FirmwareUpdate{URI: "coaps://example/x"}
	if err := ValidateFirmwareUpdate(&u); err != nil || u.Method != "auto" || u.PollSeconds != 30 {
		t.Fatalf("%+v %v", u, err)
	}
}
