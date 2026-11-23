package storetest

import (
	"bytes"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// FirmwareContent verifies isolation, atomic upload and owned snapshots.
func FirmwareContent(t *testing.T, fresh func() apiv1.FirmwareStore) {
	t.Helper()
	s := fresh()
	c, ok := s.(apiv1.FirmwareContentStore)
	if !ok {
		t.Fatal("content unsupported")
	}
	_ = s.Create("a", apiv1.Firmware{ID: "fw", Status: "empty"})
	_ = s.Create("b", apiv1.Firmware{ID: "fw", Status: "empty"})
	data := []byte{1, 2, 3}
	if found, conflict := c.PutFirmwareContent("a", "fw", data); !found || conflict {
		t.Fatal(found, conflict)
	}
	data[0] = 9
	got, ok := c.GetFirmwareContent("a", "fw")
	if !ok || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatal(got, ok)
	}
	got[0] = 9
	got, _ = c.GetFirmwareContent("a", "fw")
	if got[0] != 1 {
		t.Fatal("aliased read")
	}
	if _, ok := c.GetFirmwareContent("b", "fw"); ok {
		t.Fatal("cross-tenant read")
	}
	if found, conflict := c.PutFirmwareContent("a", "fw", data); !found || !conflict {
		t.Fatal(found, conflict)
	}
	if found, _ := c.PutFirmwareContent("b", "missing", data); found {
		t.Fatal("missing accepted")
	}
	s.Delete("a", "fw")
	if _, ok := c.GetFirmwareContent("a", "fw"); ok {
		t.Fatal("content survived deletion")
	}
}
