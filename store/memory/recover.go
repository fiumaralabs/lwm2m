package memory

import (
	"context"
	"sort"

	"github.com/fiumaralabs/lwm2m/operations"
)

var _ operations.Recoverer = (*Operations)(nil)

// Unfinished snapshots queued and sent operations across every tenant.
func (s *Operations) Unfinished(ctx context.Context) ([]operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []operations.Operation{}
	for _, o := range s.records {
		if o.Status == operations.Queued || o.Status == operations.Sent {
			out = append(out, cloneOperation(o))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
