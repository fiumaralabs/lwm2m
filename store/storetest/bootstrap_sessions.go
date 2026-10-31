package storetest

import (
	"fmt"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func bootstrapSessionOrdering(t *testing.T, s apiv1.BootstrapSessionStore) {
	for i := 0; i < 3; i++ {
		err := s.Append("tenant", "device", apiv1.BootstrapSession{ID: fmt.Sprintf("bs_%d", i), Kind: "request", Status: "succeeded"})
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

func bootstrapSessionValidation(t *testing.T, s apiv1.BootstrapSessionStore) {
	tests := []struct {
		kind, status, failure string
		valid                 bool
	}{
		{"request", "succeeded", "", true}, {"pack", "failed", `"finish_rejected"`, true},
		{"bad", "succeeded", "", false}, {"request", "unknown", "", false},
		{"request", "failed", "null", false}, {"request", "failed", " null ", false},
		{"request", "succeeded", `"problem"`, false}, {"request", "failed", "not-json", false},
	}
	for _, tc := range tests {
		err := s.Append("t", "e", apiv1.BootstrapSession{Kind: tc.kind, Status: tc.status, Error: []byte(tc.failure)})
		if (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func bootstrapSessionRetentionAndIsolation(t *testing.T, s apiv1.BootstrapSessionStore) {
	for i := 0; i < apiv1.BootstrapSessionRetention+2; i++ {
		if err := s.Append("t", "e", apiv1.BootstrapSession{ID: fmt.Sprintf("bs_%d", i), Kind: "pack", Status: "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range [][2]string{{"other", "e"}, {"t", "other"}} {
		if err := s.Append(key[0], key[1], apiv1.BootstrapSession{ID: "bs_other", Kind: "request", Status: "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := s.ListForDevice("t", "e", "", 500)
	if err != nil || len(got) != apiv1.BootstrapSessionRetention || got[0].ID != fmt.Sprintf("bs_%d", apiv1.BootstrapSessionRetention+1) || got[len(got)-1].ID != "bs_2" {
		t.Fatalf("retention: %v %v", got, err)
	}
	for _, key := range [][2]string{{"other", "e"}, {"t", "other"}} {
		got, _, err := s.ListForDevice(key[0], key[1], "", 1)
		if err != nil || len(got) != 1 || got[0].ID != "bs_other" {
			t.Fatalf("isolation: %v %v", got, err)
		}
	}
}

func bootstrapSessionCursorAndCopies(t *testing.T, s apiv1.BootstrapSessionStore) {
	original := apiv1.BootstrapSession{ID: "bs_one", Kind: "request", Status: "succeeded", Identity: []byte(`{"mode":"psk"}`), Steps: []apiv1.BootstrapSessionStep{{Path: "/0"}}, Discover: []byte(`[]`), Reads: []byte(`{}`)}
	if err := s.Append("t", "e", original); err != nil {
		t.Fatal(err)
	}
	original.Identity[2] = 'X'
	original.Steps[0].Path = "changed"
	if err := s.Append("t", "e", apiv1.BootstrapSession{ID: "bs_two", Kind: "pack", Status: "succeeded"}); err != nil {
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
		if _, _, err := s.ListForDevice(args.tenant, args.endpoint, args.cursor, args.limit); err != apiv1.ErrBootstrapSessionCursor {
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

func BootstrapSessions(t *testing.T, fresh func() apiv1.BootstrapSessionStore) {
	t.Run("EmptySteps", func(t *testing.T) { bootstrapSessionEmptySteps(t, fresh()) })
	t.Run("Ordering", func(t *testing.T) { bootstrapSessionOrdering(t, fresh()) })
	t.Run("Validation", func(t *testing.T) { bootstrapSessionValidation(t, fresh()) })
	t.Run("RetentionAndIsolation", func(t *testing.T) { bootstrapSessionRetentionAndIsolation(t, fresh()) })
	t.Run("CursorAndCopies", func(t *testing.T) { bootstrapSessionCursorAndCopies(t, fresh()) })
}

func bootstrapSessionEmptySteps(t *testing.T, s apiv1.BootstrapSessionStore) {
	original := apiv1.BootstrapSession{ID: "empty", Kind: "pack", Status: "succeeded", Steps: []apiv1.BootstrapSessionStep{}}
	if err := s.Append("t", "e", original); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.ListForDevice("t", "e", "", 1)
	if err != nil || len(got) != 1 || got[0].Steps == nil {
		t.Fatalf("empty steps not preserved: %+v %v", got, err)
	}
}
