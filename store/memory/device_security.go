package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

// Security returns the credential store sharing the device transaction lock.
// The server must use this same store for credential lookups and mutations.
func (s *Devices) Security() server.SecurityStore { return memorySecurity{s} }

// OwnsSecurity reports whether security shares this device store's lock.
func (s *Devices) OwnsSecurity(security server.SecurityStore) bool {
	m, ok := security.(memorySecurity)
	return ok && m.devices == s
}

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
	return s.createDeviceWithSecurity(ctx, tenant, id, fingerprint, d, security, nil)
}
func (s *Devices) CreateDeviceWithSecurityAndBootstrap(ctx context.Context, tenant, id, fingerprint string, d store.Device, security server.SecurityInfo, config json.RawMessage) (store.DeviceCreateOutcome, bool, error) {
	if config == nil {
		return store.DeviceCreateOutcome{}, false, errors.New("store: missing bootstrap config")
	}
	return s.createDeviceWithSecurity(ctx, tenant, id, fingerprint, d, security, config)
}
func (s *Devices) createDeviceWithSecurity(ctx context.Context, tenant, id, fingerprint string, d store.Device, security server.SecurityInfo, config json.RawMessage) (store.DeviceCreateOutcome, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	if o, ok := s.creates[tenant][id]; ok {
		o, err := replayOutcome(o, fingerprint)
		return o, false, err
	}
	hasSecurity := !reflect.DeepEqual(security, server.SecurityInfo{})
	if !hasSecurity && config == nil || hasSecurity && security.Endpoint != d.Endpoint {
		return store.DeviceCreateOutcome{}, false, errors.New("store: security endpoint mismatch")
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
		return store.DeviceCreateOutcome{}, false, store.ErrCredentialExists
	}
	if config != nil {
		if _, ok := s.bootstrapConfigs[tenant][d.Endpoint]; ok {
			return store.DeviceCreateOutcome{}, false, store.ErrBootstrapConfigExists
		}
	}
	d, err := s.createLocked(tenant, d)
	if err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	rollback := func() { delete(s.records[tenant], d.Endpoint) }
	if s.deviceSecurityHook != nil {
		if err := s.deviceSecurityHook("after-device"); err != nil {
			rollback()
			return store.DeviceCreateOutcome{}, false, err
		}
	}
	if hasSecurity {
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
	}
	if config != nil {
		s.putBootstrapLocked(tenant, d.Endpoint, config)
		if s.deviceSecurityHook != nil {
			if err := s.deviceSecurityHook("after-bootstrap"); err != nil {
				delete(s.bootstrapConfigs[tenant], d.Endpoint)
				sec.Remove(d.Endpoint)
				rollback()
				return store.DeviceCreateOutcome{}, false, err
			}
		}
	}
	o := newOutcome(tenant, id, fingerprint, "committed", "")
	o.Endpoint = d.Endpoint
	if store.DeviceCreateReplayFrom(ctx) == nil {
		s.saveOutcome(o)
	}
	s.saveCreateReplay(ctx, tenant, d)
	store.CaptureDeviceCreate(ctx, clone(d))
	return o, true, nil
}

func (s *Devices) putBootstrapLocked(tenant, endpoint string, config json.RawMessage) {
	if s.bootstrapConfigs == nil {
		s.bootstrapConfigs = make(map[string]map[string]json.RawMessage)
	}
	if s.bootstrapConfigs[tenant] == nil {
		s.bootstrapConfigs[tenant] = make(map[string]json.RawMessage)
	}
	s.bootstrapConfigs[tenant][endpoint] = bytes.Clone(config)
}
func (s *Devices) PutBootstrapConfig(ctx context.Context, tenant, endpoint string, config json.RawMessage) error {
	if config == nil {
		return errors.New("store: missing bootstrap config")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.putBootstrapLocked(tenant, endpoint, config)
	return nil
}
func (s *Devices) GetBootstrapConfig(ctx context.Context, tenant, endpoint string) (json.RawMessage, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	b, ok := s.bootstrapConfigs[tenant][endpoint]
	return bytes.Clone(b), ok, nil
}

func (s *Devices) DeleteBootstrapConfig(ctx context.Context, tenant, endpoint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ok := s.bootstrapConfigs[tenant][endpoint]
	delete(s.bootstrapConfigs[tenant], endpoint)
	return ok, nil
}
