package memory

import (
	"context"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

var _ store.DeviceCreateStore = (*Devices)(nil)

func (s *Devices) saveOutcome(o store.DeviceCreateOutcome) {
	if s.creates == nil {
		s.creates = make(map[string]map[string]store.DeviceCreateOutcome)
	}
	if s.creates[o.Tenant] == nil {
		s.creates[o.Tenant] = make(map[string]store.DeviceCreateOutcome)
	}
	s.creates[o.Tenant][o.OperationID] = o
}

func replayOutcome(o store.DeviceCreateOutcome, fingerprint string) (store.DeviceCreateOutcome, error) {
	if o.Reason != "fenced" && o.Fingerprint != fingerprint {
		return store.DeviceCreateOutcome{}, store.ErrOperationIDReused
	}
	return o, nil
}

func newOutcome(tenant, id, fingerprint, status, reason string) store.DeviceCreateOutcome {
	now := time.Now().UTC()
	return store.DeviceCreateOutcome{Tenant: tenant, OperationID: id, Fingerprint: fingerprint, Status: status, Reason: reason, CreatedAt: now, CompletedAt: now}
}

func (s *Devices) CreateDeviceWithOutcome(ctx context.Context, tenant, id, fingerprint string, d store.Device) (store.DeviceCreateOutcome, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	if o, ok := s.creates[tenant][id]; ok {
		o, err := replayOutcome(o, fingerprint)
		return o, false, err
	}
	for _, devices := range s.records {
		if _, exists := devices[d.Endpoint]; exists {
			o := newOutcome(tenant, id, fingerprint, "not-created", "endpoint-conflict")
			s.saveOutcome(o)
			return o, false, nil
		}
	}
	_, err := s.createLocked(tenant, d)
	if err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	o := newOutcome(tenant, id, fingerprint, "committed", "")
	o.Endpoint = d.Endpoint
	s.saveOutcome(o)
	return o, true, nil
}

func (s *Devices) RecordDeviceCreateNotCreated(ctx context.Context, tenant, id, fingerprint, reason string) (store.DeviceCreateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.DeviceCreateOutcome{}, err
	}
	if o, ok := s.creates[tenant][id]; ok {
		return replayOutcome(o, fingerprint)
	}
	o := newOutcome(tenant, id, fingerprint, "not-created", reason)
	s.saveOutcome(o)
	return o, nil
}

func (s *Devices) GetOrFenceDeviceCreate(ctx context.Context, tenant, id string) (store.DeviceCreateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.DeviceCreateOutcome{}, err
	}
	if o, ok := s.creates[tenant][id]; ok {
		return o, nil
	}
	o := newOutcome(tenant, id, "", "not-created", "fenced")
	s.saveOutcome(o)
	return o, nil
}

func (s *Devices) DeleteDeviceCreate(ctx context.Context, tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := s.creates[tenant][id]; !ok {
		return store.ErrNotFound
	}
	delete(s.creates[tenant], id)
	return nil
}
