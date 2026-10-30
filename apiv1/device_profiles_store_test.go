package apiv1

import (
	"encoding/json"
	"testing"
)

func TestDeviceProfileMemoryStore(t *testing.T) {
	s := NewMemoryDeviceProfileStore()
	p := DeviceProfile{ID: "dp_1", Name: "pump", Observations: json.RawMessage(`[{"paths":["/3/0/1"]}]`)}
	if err := s.Create("a", p); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("a", p); err == nil {
		t.Fatal("duplicate ID must not overwrite an existing profile")
	}
	p.Observations[0] = 'x'
	got, ok := s.Get("a", "dp_1")
	if !ok || string(got.Observations) != `[{"paths":["/3/0/1"]}]` {
		t.Fatalf("snapshot: %+v", got)
	}
	got.Observations[0] = 'x'
	if _, ok := s.Get("b", "dp_1"); ok {
		t.Fatal("foreign tenant read")
	}
	if len(s.List("b")) != 0 {
		t.Fatal("foreign tenant list")
	}
	got, _ = s.Get("a", "dp_1")
	if string(got.Observations) != `[{"paths":["/3/0/1"]}]` {
		t.Fatal("read alias")
	}
	got.Name = "updated"
	if !s.Replace("a", got) {
		t.Fatal("replace")
	}
	if s.Replace("b", got) {
		t.Fatal("foreign tenant replace")
	}
	got, _ = s.Get("a", "dp_1")
	if got.Name != "updated" {
		t.Fatal(got)
	}
	if found, _ := s.Delete("b", "dp_1"); found {
		t.Fatal("foreign tenant delete")
	}
	if found, inUse := s.Delete("a", "dp_1"); !found || inUse {
		t.Fatal(found, inUse)
	}
	if found, _ := s.Delete("a", "dp_1"); found {
		t.Fatal("duplicate delete")
	}
}
