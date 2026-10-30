package apiv1

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// FirmwareUpdate describes a device firmware update and its observed result.
type FirmwareUpdate struct {
	ID                 string                  `json:"id"`
	Device             string                  `json:"device"`
	Firmware           string                  `json:"firmware,omitempty"`
	URI                string                  `json:"uri,omitempty"`
	Method             string                  `json:"method"`
	Severity           string                  `json:"severity,omitempty"`
	MaxDeferSeconds    *int                    `json:"maxDeferSeconds,omitempty"`
	AutoUpgrade        bool                    `json:"autoUpgrade"`
	Resume             bool                    `json:"resume"`
	PushTimeoutSeconds int                     `json:"pushTimeoutSeconds"`
	PushAttempts       int                     `json:"pushAttempts"`
	PollSeconds        int                     `json:"pollSeconds"`
	Status             string                  `json:"status"`
	DeviceState        string                  `json:"deviceState"`
	UpdateResult       *FirmwareUpdateResult   `json:"updateResult"`
	Progress           *FirmwareUpdateProgress `json:"progress"`
	CreatedAt          time.Time               `json:"createdAt"`
	CompletedAt        *time.Time              `json:"completedAt"`
	Outcome            *FirmwareUpdateOutcome  `json:"outcome"`
}
type FirmwareUpdateResult struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}
type FirmwareUpdateProgress struct {
	BytesSent  int64 `json:"bytesSent"`
	TotalBytes int64 `json:"totalBytes"`
}
type FirmwareUpdateOutcome struct {
	ObjectVersion string   `json:"objectVersion"`
	Added         []string `json:"added"`
	Removed       []string `json:"removed"`
}

// ErrUpdateInProgress indicates that a device already has an active update.
var ErrUpdateInProgress = errors.New("firmware update in progress")

// FirmwareUpdateStore isolates updates by tenant. IDs are supplied by the caller.
type FirmwareUpdateStore interface {
	Create(string, FirmwareUpdate) error
	Get(string, string) (FirmwareUpdate, bool)
	ListForDevice(string, string) []FirmwareUpdate
	UpdateStatus(string, string, string) bool
}
type memoryFirmwareUpdateStore struct {
	mu      sync.RWMutex
	updates map[string]map[string]FirmwareUpdate
}

func NewMemoryFirmwareUpdateStore() FirmwareUpdateStore {
	return &memoryFirmwareUpdateStore{updates: make(map[string]map[string]FirmwareUpdate)}
}

// ValidateFirmwareUpdate applies request defaults and validates configuration.
func ValidateFirmwareUpdate(u *FirmwareUpdate) error {
	if u == nil || (strings.TrimSpace(u.Firmware) == "") == (strings.TrimSpace(u.URI) == "") {
		return errors.New("exactly one firmware or uri is required")
	}
	if u.Method == "" {
		u.Method = "auto"
	}
	if u.Method != "auto" && u.Method != "push" && u.Method != "pull" {
		return errors.New("invalid firmware update method")
	}
	if u.Severity != "" && u.Severity != "critical" && u.Severity != "mandatory" && u.Severity != "optional" {
		return errors.New("invalid firmware update severity")
	}
	if (u.MaxDeferSeconds != nil && *u.MaxDeferSeconds < 0) || u.PushTimeoutSeconds < 0 || u.PushAttempts < 0 || u.PollSeconds < 0 {
		return errors.New("invalid firmware update settings")
	}
	if u.PushTimeoutSeconds == 0 {
		u.PushTimeoutSeconds = 300
	}
	if u.PushAttempts == 0 {
		u.PushAttempts = 1
	}
	if u.PollSeconds == 0 {
		u.PollSeconds = 30
	}
	return nil
}
func cloneFirmwareUpdate(u FirmwareUpdate) FirmwareUpdate {
	if u.MaxDeferSeconds != nil {
		v := *u.MaxDeferSeconds
		u.MaxDeferSeconds = &v
	}
	if u.UpdateResult != nil {
		v := *u.UpdateResult
		u.UpdateResult = &v
	}
	if u.Progress != nil {
		v := *u.Progress
		u.Progress = &v
	}
	if u.CompletedAt != nil {
		v := *u.CompletedAt
		u.CompletedAt = &v
	}
	if u.Outcome != nil {
		v := *u.Outcome
		if v.Added != nil {
			v.Added = append([]string{}, v.Added...)
		}
		if v.Removed != nil {
			v.Removed = append([]string{}, v.Removed...)
		}
		u.Outcome = &v
	}
	return u
}
func terminalFirmwareUpdate(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}
func (s *memoryFirmwareUpdateStore) Create(tenant string, u FirmwareUpdate) error {
	if err := ValidateFirmwareUpdate(&u); err != nil {
		return err
	}
	if !strings.HasPrefix(u.ID, "fwu_") || len(u.ID) == 4 || strings.TrimSpace(u.Device) == "" {
		return errors.New("invalid firmware update identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.updates[tenant][u.ID]; ok {
		return errors.New("firmware update already exists")
	}
	for _, existing := range s.updates[tenant] {
		if existing.Device == u.Device && !terminalFirmwareUpdate(existing.Status) {
			return ErrUpdateInProgress
		}
	}
	if s.updates[tenant] == nil {
		s.updates[tenant] = make(map[string]FirmwareUpdate)
	}
	u.Status = "pending"
	u.CompletedAt = nil
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	s.updates[tenant][u.ID] = cloneFirmwareUpdate(u)
	return nil
}
func (s *memoryFirmwareUpdateStore) Get(tenant, id string) (FirmwareUpdate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.updates[tenant][id]
	return cloneFirmwareUpdate(u), ok
}
func (s *memoryFirmwareUpdateStore) ListForDevice(tenant, device string) []FirmwareUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []FirmwareUpdate{}
	for _, u := range s.updates[tenant] {
		if u.Device == device {
			out = append(out, cloneFirmwareUpdate(u))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}
func validFirmwareUpdateTransition(from, to string) bool {
	if terminalFirmwareUpdate(from) {
		return false
	}
	switch from {
	case "pending":
		return to == "downloading" || to == "downloaded" || to == "updating" || to == "failed" || to == "deferred" || to == "cancelled"
	case "downloading":
		return to == "downloaded" || to == "updating" || to == "failed" || to == "deferred" || to == "cancelled"
	case "downloaded":
		return to == "updating" || to == "succeeded" || to == "failed" || to == "deferred" || to == "cancelled"
	case "updating":
		return to == "succeeded" || to == "failed" || to == "deferred" || to == "cancelled"
	case "deferred":
		return to == "pending" || to == "downloading" || to == "downloaded" || to == "updating" || to == "failed" || to == "cancelled"
	}
	return false
}
func (s *memoryFirmwareUpdateStore) UpdateStatus(tenant, id, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.updates[tenant][id]
	if !ok || !validFirmwareUpdateTransition(u.Status, status) {
		return false
	}
	u.Status = status
	if terminalFirmwareUpdate(status) {
		now := time.Now().UTC()
		u.CompletedAt = &now
	}
	s.updates[tenant][id] = u
	return true
}
