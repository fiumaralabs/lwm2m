// Package memory provides process-local stores. Zero values are ready to use.
package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

type Operations struct {
	mu      sync.Mutex
	records map[[2]string]operations.Operation
	byTime  map[string][]operationTime
}

// operationTime indexes immutable creation timestamps in ascending order.
type operationTime struct {
	at time.Time
	id string
}

func NewOperations() *Operations { return &Operations{} }

func cloneOperation(o operations.Operation) operations.Operation {
	o.Request = append([]byte(nil), o.Request...)
	o.Result = append([]byte(nil), o.Result...)
	if o.Error != nil {
		v := *o.Error
		o.Error = &v
	}
	if o.Job != nil {
		v := *o.Job
		o.Job = &v
	}
	if o.SentAt != nil {
		v := *o.SentAt
		o.SentAt = &v
	}
	if o.CompletedAt != nil {
		v := *o.CompletedAt
		o.CompletedAt = &v
	}
	return o
}

func (s *Operations) Create(ctx context.Context, tenant string, o operations.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant != o.Tenant {
		return store.ErrTenant
	}
	key := [2]string{tenant, o.ID}
	if _, ok := s.records[key]; ok {
		return store.ErrExists
	}
	if s.records == nil {
		s.records = make(map[[2]string]operations.Operation)
		s.byTime = make(map[string][]operationTime)
	}
	s.records[key] = cloneOperation(o)
	index := s.byTime[tenant]
	i := sort.Search(len(index), func(i int) bool { return index[i].at.After(o.CreatedAt) })
	index = append(index, operationTime{})
	copy(index[i+1:], index[i:])
	index[i] = operationTime{at: o.CreatedAt, id: o.ID}
	s.byTime[tenant] = index
	return nil
}

func (s *Operations) Get(ctx context.Context, tenant, id string) (operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	o, ok := s.records[[2]string{tenant, id}]
	if !ok {
		return operations.Operation{}, store.ErrNotFound
	}
	return cloneOperation(o), nil
}

type operationCursor struct {
	Tenant, Filter, ID string
	At                 time.Time
}

func (s *Operations) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	f.Status = slices.Clone(f.Status)
	slices.Sort(f.Status)
	f.Status = slices.Compact(f.Status)
	if len(f.Status) == 0 {
		f.Status = nil
	}
	query, _ := json.Marshal(f)
	c := operationCursor{Tenant: tenant, Filter: string(query)}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.Filter != string(query) || c.ID == "" {
			return nil, "", store.ErrCursor
		}
	}
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrCursor
	}
	items, err := s.list(ctx, tenant, f.Device, false, f.CreatedAfter, f.CreatedBefore)
	if err != nil {
		return nil, "", err
	}
	out := make([]operations.Operation, 0)
	for _, o := range items {
		if (f.Type != "" && o.Type != f.Type) || (f.Job != "" && (o.Job == nil || *o.Job != f.Job)) || (len(f.Status) != 0 && !slices.Contains(f.Status, o.Status)) {
			continue
		}
		if c.ID != "" && (o.CreatedAt.After(c.At) || (o.CreatedAt.Equal(c.At) && o.ID >= c.ID)) {
			continue
		}
		if len(out) == limit {
			last := out[len(out)-1]
			c.ID, c.At = last.ID, last.CreatedAt
			b, _ := json.Marshal(c)
			return out, base64.RawURLEncoding.EncodeToString(b), nil
		}
		out = append(out, o)
	}
	return out, "", nil
}
func (s *Operations) Pending(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	return s.list(ctx, tenant, device, true, nil, nil)
}
func (s *Operations) list(ctx context.Context, tenant, device string, pending bool, after, before *time.Time) ([]operations.Operation, error) {
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	index := s.byTime[tenant]
	start, end := 0, len(index)
	if after != nil {
		start = sort.Search(len(index), func(i int) bool { return index[i].at.After(*after) })
	}
	if before != nil {
		end = sort.Search(len(index), func(i int) bool { return index[i].at.After(*before) })
	}
	if end < start {
		end = start
	}
	out := []operations.Operation{}
	for _, entry := range index[start:end] {
		o := s.records[[2]string{tenant, entry.id}]
		if (device == "" || device == o.Device) && (!pending || o.Status == operations.Queued) {
			out = append(out, cloneOperation(o))
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !pending {
			a, b = b, a
		}
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out, nil
}
func (s *Operations) Update(ctx context.Context, tenant, id string, expected operations.Status, change operations.Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key := [2]string{tenant, id}
	o, ok := s.records[key]
	if !ok {
		return store.ErrNotFound
	}
	if o.Status != expected {
		return operations.ErrTransition
	}
	if err := o.Apply(change); err != nil {
		return err
	}
	s.records[key] = cloneOperation(o)
	return nil
}
