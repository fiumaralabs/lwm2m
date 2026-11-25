package memory

import (
	"context"
	"errors"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

// Security returns the credential store sharing the device transaction lock.
// The server must use this same store for credential lookups and mutations.
func (s *Devices) Security() server.SecurityStore { return memorySecurity{s} }

type memorySecurity struct{ devices *Devices }

func (s *Devices) securityLocked() *server.MemorySecurityStore {
	if s.security == nil {
		s.security = server.NewMemorySecurityStore()
	}
	return s.security
}
func copySecurity(v server.SecurityInfo) server.SecurityInfo {
	v.PSKKey = append([]byte(nil), v.PSKKey...)
	v.PublicKey = append([]byte(nil), v.PublicKey...)
	return v
}
func (m memorySecurity) Put(v server.SecurityInfo) error {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	return m.devices.securityLocked().Put(copySecurity(v))
}
func (m memorySecurity) ByEndpoint(ep string) (server.SecurityInfo, bool) {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	v, ok := m.devices.securityLocked().ByEndpoint(ep)
	return copySecurity(v), ok
}
func (m memorySecurity) ByPSKIdentity(id string) (server.SecurityInfo, bool) {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	v, ok := m.devices.securityLocked().ByPSKIdentity(id)
	return copySecurity(v), ok
}
func (m memorySecurity) Remove(ep string) (server.SecurityInfo, bool, error) {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	v, ok, err := m.devices.securityLocked().Remove(ep)
	return copySecurity(v), ok, err
}
func (m memorySecurity) All() []server.SecurityInfo {
	m.devices.mu.Lock()
	defer m.devices.mu.Unlock()
	all := m.devices.securityLocked().All()
	for i := range all {
		all[i] = copySecurity(all[i])
	}
	return all
}

func (s *Devices) CreateDeviceWithSecurity(ctx context.Context, tenant, id, fingerprint string, d store.Device, security server.SecurityInfo) (store.DeviceCreateOutcome, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	if security.Endpoint != d.Endpoint {
		return store.DeviceCreateOutcome{}, false, errors.New("store: security endpoint mismatch")
	}
	if o, ok := s.creates[tenant][id]; ok {
		o, err := replayOutcome(o, fingerprint)
		return o, false, err
	}
	for _, devices := range s.records {
		if _, ok := devices[d.Endpoint]; ok {
			o := newOutcome(tenant, id, fingerprint, "not-created", "endpoint-conflict")
			s.saveOutcome(o)
			return o, true, nil
		}
	}
	sec := s.securityLocked()
	if _, ok := sec.ByEndpoint(d.Endpoint); ok {
		return store.DeviceCreateOutcome{}, false, errors.New("store: endpoint credential already exists")
	}
	if _, err := s.createLocked(tenant, d); err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	rollback := func() { delete(s.records[tenant], d.Endpoint) }
	if s.deviceSecurityHook != nil {
		if err := s.deviceSecurityHook("after-device"); err != nil {
			rollback()
			return store.DeviceCreateOutcome{}, false, err
		}
	}
	if err := sec.Put(copySecurity(security)); err != nil {
		rollback()
		return store.DeviceCreateOutcome{}, false, err
	}
	if s.deviceSecurityHook != nil {
		if err := s.deviceSecurityHook("after-security"); err != nil {
			sec.Remove(d.Endpoint)
			rollback()
			return store.DeviceCreateOutcome{}, false, err
		}
	}
	o := newOutcome(tenant, id, fingerprint, "committed", "")
	o.Endpoint = d.Endpoint
	s.saveOutcome(o)
	return o, true, nil
}
