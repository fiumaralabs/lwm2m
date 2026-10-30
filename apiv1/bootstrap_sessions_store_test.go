package apiv1

import (
	"fmt"
	"testing"
)

func TestBootstrapSessionOrdering(t *testing.T) {
	s := NewMemoryBootstrapSessionStore()
	for i := 0; i < 3; i++ {
		err := s.Append("tenant", "device", BootstrapSession{ID: fmt.Sprintf("bs_%d", i), Kind: "request", Status: "succeeded"})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, next, err := s.ListForDevice("tenant", "device", "", 2)
	if err != nil || len(first) != 2 || first[0].ID != "bs_2" || first[1].ID != "bs_1" || next == "" {
		t.Fatalf("first page: %+v %q %v", first, next, err)
	}
	second, end, err := s.ListForDevice("tenant", "device", next, 2)
	if err != nil || len(second) != 1 || second[0].ID != "bs_0" || end != "" {
		t.Fatalf("second page: %+v %q %v", second, end, err)
	}
}

func TestBootstrapSessionValidation(t *testing.T) {
	tests := []struct {
		kind, status, failure string
		valid                 bool
	}{
		{"request", "succeeded", "", true}, {"pack", "failed", `"finish_rejected"`, true},
		{"bad", "succeeded", "", false}, {"request", "unknown", "", false},
		{"request", "failed", "null", false}, {"request", "failed", " null ", false},
		{"request", "succeeded", `"problem"`, false}, {"request", "failed", "not-json", false},
	}
	s := NewMemoryBootstrapSessionStore()
	for _, tc := range tests {
		err := s.Append("t", "e", BootstrapSession{Kind: tc.kind, Status: tc.status, Error: []byte(tc.failure)})
		if (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func TestBootstrapSessionRetentionAndIsolation(t *testing.T) {
	s := NewMemoryBootstrapSessionStore()
	for i := 0; i < BootstrapSessionRetention+2; i++ {
		if err := s.Append("t", "e", BootstrapSession{ID: fmt.Sprintf("bs_%d", i), Kind: "pack", Status: "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range [][2]string{{"other", "e"}, {"t", "other"}} {
		if err := s.Append(key[0], key[1], BootstrapSession{ID: "bs_other", Kind: "request", Status: "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := s.ListForDevice("t", "e", "", 500)
	if err != nil || len(got) != BootstrapSessionRetention || got[0].ID != fmt.Sprintf("bs_%d", BootstrapSessionRetention+1) || got[len(got)-1].ID != "bs_2" {
		t.Fatalf("retention: %v %v", got, err)
	}
	for _, key := range [][2]string{{"other", "e"}, {"t", "other"}} {
		got, _, err := s.ListForDevice(key[0], key[1], "", 1)
		if err != nil || len(got) != 1 || got[0].ID != "bs_other" {
			t.Fatalf("isolation: %v %v", got, err)
		}
	}
}

func TestBootstrapSessionCursorAndCopies(t *testing.T) {
	s := NewMemoryBootstrapSessionStore()
	original := BootstrapSession{ID: "bs_one", Kind: "request", Status: "succeeded", Identity: []byte(`{"mode":"psk"}`), Steps: []BootstrapSessionStep{{Path: "/0"}}, Discover: []byte(`[]`), Reads: []byte(`{}`)}
	if err := s.Append("t", "e", original); err != nil {
		t.Fatal(err)
	}
	original.Identity[2] = 'X'
	original.Steps[0].Path = "changed"
	if err := s.Append("t", "e", BootstrapSession{ID: "bs_two", Kind: "pack", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	_, cursor, err := s.ListForDevice("t", "e", "", 1)
	if err != nil || cursor == "" {
		t.Fatal(cursor, err)
	}
	for _, args := range []struct {
		tenant, endpoint, cursor string
		limit                    int
	}{{"x", "e", cursor, 1}, {"t", "x", cursor, 1}, {"t", "e", "garbage", 1}, {"t", "e", "", 0}, {"t", "e", "", 501}} {
		if _, _, err := s.ListForDevice(args.tenant, args.endpoint, args.cursor, args.limit); err != ErrBootstrapSessionCursor {
			t.Errorf("cursor: %+v %v", args, err)
		}
	}
	got, _, err := s.ListForDevice("t", "e", cursor, 1)
	if err != nil || len(got) != 1 || string(got[0].Identity) != `{"mode":"psk"}` || got[0].Steps[0].Path != "/0" {
		t.Fatalf("owned input: %v %v", got, err)
	}
	got[0].Identity[2] = 'X'
	got[0].Steps[0].Path = "changed"
	got, _, _ = s.ListForDevice("t", "e", cursor, 1)
	if string(got[0].Identity) != `{"mode":"psk"}` || got[0].Steps[0].Path != "/0" {
		t.Fatal("stored value mutated")
	}
}
