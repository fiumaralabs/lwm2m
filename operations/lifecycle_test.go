package operations_test

import (
	"github.com/fiumaralabs/lwm2m/operations"
	"testing"
	"time"
)

func TestRecoveryLiveTimer(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "expires", true: "close_stops_timer"}[closed], func(t *testing.T) {
			h := setup(t, operations.ExecutorOptions{})
			o := operations.New("t", "dev", "read", []byte(`{"paths":["/3/0/0"]}`), nil, time.Now())
			o.ExpiresAt = time.Now().Add(60 * time.Millisecond)
			if err := h.st.Create(h.ctx, "t", o); err != nil {
				t.Fatal(err)
			}
			if err := h.e.Start(h.ctx); err != nil {
				t.Fatal(err)
			}
			defer h.e.Close()
			if closed {
				h.e.Close()
				time.Sleep(100 * time.Millisecond)
				got, err := h.st.Get(h.ctx, "t", o.ID)
				if err != nil || got.Status != operations.Queued {
					t.Fatalf("timer ran after Close: %+v %v", got, err)
				}
			} else {
				h.wait(t, o, operations.Expired)
			}
		})
	}
}

func TestRecoveryRegistered(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	for _, policy := range []string{"queue", "reject"} {
		o := operations.New("t", "dev", "read", []byte(`{"paths":["/3/0/0"]}`), nil, time.Now())
		o.WhenOffline = policy
		if err := h.st.Create(h.ctx, "t", o); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.e.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	defer h.e.Close()
	rows, _ := h.st.Pending(h.ctx, "t", "dev")
	for _, o := range rows {
		h.wait(t, o, operations.Succeeded)
	}
	deadline := time.Now().Add(time.Second)
	for len(c.Requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(c.Requests()) != 2 {
		t.Fatalf("requests=%v", c.Requests())
	}
}
