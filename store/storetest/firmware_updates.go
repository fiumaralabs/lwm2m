package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func FirmwareUpdates(t *testing.T, fresh func() apiv1.FirmwareUpdateStore) {
	t.Helper()
	t.Run("CreateAndIsolation", func(t *testing.T) {
		s := fresh()
		u := apiv1.FirmwareUpdate{ID: "fwu_a", Device: "d", Firmware: "fw"}
		if err := s.Create("a", u); err != nil {
			t.Fatal(err)
		}
		got, ok := s.Get("a", u.ID)
		if !ok || got.Status != "pending" || got.Method != "auto" || got.CreatedAt.IsZero() {
			t.Fatalf("got %+v", got)
		}
		if _, ok := s.Get("b", u.ID); ok {
			t.Fatal("tenant leak")
		}
		if err := s.Create("a", u); err == nil {
			t.Fatal("duplicate accepted")
		}
		u.ID = "fwu_b"
		if !errors.Is(s.Create("a", u), apiv1.ErrUpdateInProgress) {
			t.Fatal("active conflict missing")
		}
		if err := s.Create("b", u); err != nil {
			t.Fatal(err)
		}
		if !s.UpdateStatus("a", "fwu_a", "failed") || s.UpdateStatus("a", "fwu_a", "pending") {
			t.Fatal("terminal transition")
		}
		if err := s.Create("a", u); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ObservationsAndOrder", func(t *testing.T) {
		s := fresh()
		stamp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		u := apiv1.FirmwareUpdate{ID: "fwu_a", Device: "d", Firmware: "fw", CreatedAt: stamp}
		if err := s.Create("a", u); err != nil {
			t.Fatal(err)
		}
		obs := apiv1.FirmwareUpdateObservation{Method: "pull", Outcome: &apiv1.FirmwareUpdateOutcome{Added: []string{"x"}}}
		if !s.UpdateObserved("a", u.ID, "downloading", obs) {
			t.Fatal("observation rejected")
		}
		obs.Outcome.Added[0] = "mutated"
		got, _ := s.Get("a", u.ID)
		if got.Outcome.Added[0] != "x" {
			t.Fatal("alias")
		}
		if s.UpdateObserved("a", u.ID, "succeeded", obs) || s.UpdateObserved("a", u.ID, "downloading", apiv1.FirmwareUpdateObservation{}) {
			t.Fatal("invalid transition or method")
		}
		if !s.UpdateStatus("a", u.ID, "failed") {
			t.Fatal("failed transition")
		}
		u.ID = "fwu_b"
		if err := s.Create("a", u); err != nil {
			t.Fatal(err)
		}
		list := s.ListForDevice("a", "d")
		if len(list) != 2 || list[0].ID != "fwu_b" || list[1].ID != "fwu_a" {
			t.Fatalf("order %+v", list)
		}
	})
}
