package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/fiumaralabs/lwm2m"
)

// SyncClock writes the server's current time to the client's Current Time
// /3/0/13 (C E.4: writable by the Server for clock synchronisation).
func (s *Server) SyncClock(ctx context.Context, ep string) (*Response, error) {
	p := lwm2m.NewPath(3, 0, 13)
	return s.Write(ctx, ep, p, []lwm2m.Node{lwm2m.ValueNode(p, lwm2m.TimeOf(s.cfg.Now()))}, WriteOptions{})
}

// CheckPowerSources verifies that /3/0/6 (Available Power Sources),
// /3/0/7 (Voltage) and /3/0/8 (Current) of a Device read use the same
// resource-instance IDs per power source (C E.4, OBJ-03).
func CheckPowerSources(nodes []lwm2m.Node) error {
	ids := map[uint16][]uint16{}
	for _, n := range nodes {
		if n.Kind != lwm2m.KindValue || !n.Path.IsResourceInstance() || n.Path.Object() != 3 {
			continue
		}
		if r := n.Path.Resource(); r >= 6 && r <= 8 {
			ids[r] = append(ids[r], n.Path.ResourceInstance())
		}
	}
	base := ids[6]
	slices.Sort(base)
	for _, r := range []uint16{7, 8} {
		got := ids[r]
		if len(got) == 0 {
			continue // optional resources may be absent
		}
		slices.Sort(got)
		if !slices.Equal(got, base) {
			return fmt.Errorf("server: /3/0/%d instances %v do not match /3/0/6 %v", r, got, base)
		}
	}
	return nil
}

// DeviceErrors returns the error codes in /3/0/11; "no error" (a single
// instance 0 holding 0) yields none (C E.4).
func DeviceErrors(nodes []lwm2m.Node) []int64 {
	var out []int64
	for _, n := range nodes {
		if n.Kind == lwm2m.KindValue && n.Path.Len() == 4 && n.Path.Object() == 3 && n.Path.Resource() == 11 && n.Value.Int != 0 {
			out = append(out, n.Value.Int)
		}
	}
	return out
}
