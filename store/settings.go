package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
)

var ErrSettingsInvalid = errors.New("invalid settings")
var ErrSettingsPrecondition = errors.New("settings precondition failed")

// Settings stores tenant-isolated configuration and returns owned copies.
type Settings interface {
	Get(string) (SettingsValue, error)
	// Patch validates an RFC 7396 object and applies it with the optional strong
	// condition under the same lock. Invalid patches leave value and ETag intact.
	// Required fields cannot be deleted; defaultProfile and profile keys can.
	Patch(string, []byte, WebhookMatch) (SettingsValue, error)
}

type SettingsValue struct {
	Version      uint64 `json:"-"`
	Registration struct {
		AutoCreateDevices bool    `json:"autoCreateDevices"`
		AllowNoSec        bool    `json:"allowNoSec"`
		DefaultProfile    *string `json:"defaultProfile"`
	} `json:"registration"`
	QueueMode struct {
		SMSWakeUp bool `json:"smsWakeUp"`
	} `json:"queueMode"`
	Operations struct {
		DefaultTTLSeconds int64 `json:"defaultTtlSeconds"`
	} `json:"operations"`
	Events struct {
		RetentionDays int64 `json:"retentionDays"`
	} `json:"events"`
	RegistrationProfiles map[string][]SettingsObject `json:"registrationProfiles"`
}
type SettingsObject struct {
	ID      uint16  `json:"id"`
	Version *string `json:"version,omitempty"`
}

func (v SettingsValue) ETag() string { return `"settings-` + strconv.FormatUint(v.Version, 10) + `"` }

// ApplySettingsPatch validates and merges a patch without mutating v.
// Callers must serialize this operation with persistence of the returned value.
func ApplySettingsPatch(v SettingsValue, patch []byte, match WebhookMatch) (SettingsValue, error) {
	if match.Required && !match.Any && !slices.Contains(match.Tags, v.ETag()) {
		return SettingsValue{}, ErrSettingsPrecondition
	}
	b, _ := json.Marshal(v)
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&doc)
	var p map[string]any
	d = json.NewDecoder(bytes.NewReader(patch))
	d.UseNumber()
	if !json.Valid(patch) || d.Decode(&p) != nil || p == nil || !mergeSettings(doc, p, "") {
		return SettingsValue{}, ErrSettingsInvalid
	}
	b, _ = json.Marshal(doc)
	var out SettingsValue
	if json.Unmarshal(b, &out) != nil || out.Operations.DefaultTTLSeconds <= 0 || out.Events.RetentionDays <= 0 {
		return SettingsValue{}, ErrSettingsInvalid
	}
	out.Version = v.Version + 1
	return out, nil
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

func DefaultSettings() SettingsValue {
	v := SettingsValue{Version: 1, RegistrationProfiles: map[string][]SettingsObject{}}
	v.Operations.DefaultTTLSeconds = 86400
	v.Events.RetentionDays = 7
	return v
}
