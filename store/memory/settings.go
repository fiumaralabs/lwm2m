package memory

import (
	"bytes"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/store"
	"slices"
	"sync"
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
	v := s.current(tenant)
	if match.Required && !match.Any && !slices.Contains(match.Tags, v.ETag()) {
		return store.SettingsValue{}, store.ErrSettingsPrecondition
	}
	b, _ := json.Marshal(v)
	var doc map[string]any
	docDecoder := json.NewDecoder(bytes.NewReader(b))
	docDecoder.UseNumber()
	_ = docDecoder.Decode(&doc)
	var p map[string]any
	d := json.NewDecoder(bytes.NewReader(patch))
	d.UseNumber()
	if !json.Valid(patch) || d.Decode(&p) != nil || p == nil || !mergeSettings(doc, p, "") {
		return store.SettingsValue{}, store.ErrSettingsInvalid
	}
	b, _ = json.Marshal(doc)
	var out store.SettingsValue
	if json.Unmarshal(b, &out) != nil || out.Operations.DefaultTTLSeconds <= 0 || out.Events.RetentionDays <= 0 {
		return store.SettingsValue{}, store.ErrSettingsInvalid
	}
	out.Version = v.Version + 1
	if s.records == nil {
		s.records = map[string]store.SettingsValue{}
	}
	s.records[tenant] = out
	return cloneSettings(out), nil
}

// mergeSettings validates supplied fields while recursively applying RFC 7396.
func mergeSettings(dst, patch map[string]any, path string) bool {
	for k, v := range patch {
		if path == "/registrationProfiles" {
			if v == nil {
				delete(dst, k)
				continue
			}
			list, ok := v.([]any)
			if !ok {
				return false
			}
			for _, item := range list {
				obj, ok := item.(map[string]any)
				if !ok {
					return false
				}
				id, ok := obj["id"].(json.Number)
				if !ok {
					return false
				}
				n, err := id.Int64()
				if err != nil || n < 0 || n > 65535 {
					return false
				}
				for field, value := range obj {
					if field == "id" {
						continue
					}
					version, ok := value.(string)
					if field != "version" || !ok || version == "" {
						return false
					}
				}
			}
			dst[k] = v
			continue
		}
		old, known := dst[k]
		if !known {
			return false
		}
		if path == "/registration" && k == "defaultProfile" {
			if _, ok := v.(string); v != nil && !ok {
				return false
			}
			dst[k] = v
			continue
		}
		if v == nil {
			return false
		}
		if target, ok := old.(map[string]any); ok {
			obj, ok := v.(map[string]any)
			if !ok || !mergeSettings(target, obj, path+"/"+k) {
				return false
			}
		} else {
			dst[k] = v
		}
	}
	return true
}
