// Package memory provides process-local stores. Zero values are ready to use.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

type Operations struct {
	mu      sync.Mutex
	records map[[2]string]operations.Operation
}

func NewOperations() *Operations { return &Operations{} }

func clone(o operations.Operation) operations.Operation {
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
	}
	s.records[key] = clone(o)
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
	return clone(o), nil
}

func (s *Operations) List(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	return s.list(ctx, tenant, device, false)
}
func (s *Operations) Pending(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	return s.list(ctx, tenant, device, true)
}
func (s *Operations) list(ctx context.Context, tenant, device string, pending bool) ([]operations.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []operations.Operation{}
	for _, o := range s.records {
		if o.Tenant == tenant && (device == "" || device == o.Device) && (!pending || o.Status == operations.Queued) {
			out = append(out, clone(o))
		}
	}
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
	s.records[key] = clone(o)
	return nil
}
