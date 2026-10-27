package store

import (
	"errors"
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
func DefaultSettings() SettingsValue {
	v := SettingsValue{Version: 1, RegistrationProfiles: map[string][]SettingsObject{}}
	v.Operations.DefaultTTLSeconds = 86400
	v.Events.RetentionDays = 7
	return v
}
