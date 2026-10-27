package memory

import (
	"encoding/json"
	"sync"

	"github.com/fiumaralabs/lwm2m/store"
)

// Settings has a usable zero value; documents are serialized to own all values.
type Settings struct {
	mu      sync.Mutex
	records map[string]store.SettingsValue
}

func cloneSettings(v store.SettingsValue) store.SettingsValue {
	b, _ := json.Marshal(v)
	var out store.SettingsValue
	_ = json.Unmarshal(b, &out)
	out.Version = v.Version
	return out
}
func (s *Settings) current(tenant string) store.SettingsValue {
	if v, ok := s.records[tenant]; ok {
		return cloneSettings(v)
	}
	return store.DefaultSettings()
}
func (s *Settings) Get(tenant string) (store.SettingsValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current(tenant), nil
}
func (s *Settings) Patch(tenant string, patch []byte, match store.WebhookMatch) (store.SettingsValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := store.ApplySettingsPatch(s.current(tenant), patch, match)
	if err != nil {
		return store.SettingsValue{}, err
	}
	if s.records == nil {
		s.records = map[string]store.SettingsValue{}
	}
	s.records[tenant] = out
	return cloneSettings(out), nil
}
