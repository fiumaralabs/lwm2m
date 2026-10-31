package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

type FirmwareUpdates struct{ db *sql.DB }

func (s *Devices) FirmwareUpdates() *FirmwareUpdates { return &FirmwareUpdates{db: s.db} }

var _ apiv1.FirmwareUpdateStore = (*FirmwareUpdates)(nil)

func failFirmwareUpdate(err error) {
	if err != nil {
		panic(fmt.Errorf("firmware updates: %w", err))
	}
}

func firmwareUpdateDecode(b []byte) apiv1.FirmwareUpdate {
	var u apiv1.FirmwareUpdate
	failFirmwareUpdate(json.Unmarshal(b, &u))
	return u
}
func firmwareUpdateEncode(u apiv1.FirmwareUpdate) []byte {
	b, err := json.Marshal(u)
	failFirmwareUpdate(err)
	return b
}
func (s *FirmwareUpdates) Create(tenant string, u apiv1.FirmwareUpdate) error {
	if err := apiv1.ValidateFirmwareUpdate(&u); err != nil {
		return err
	}
	if !strings.HasPrefix(u.ID, "fwu_") || len(u.ID) == 4 || strings.TrimSpace(u.Device) == "" {
		return errors.New("invalid firmware update identity")
	}
	u.Status = "pending"
	u.CompletedAt = nil
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO firmware_updates(tenant,id,device,status,payload) VALUES(?,?,?,?,?)`, tenant, u.ID, u.Device, u.Status, firmwareUpdateEncode(u))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	var exists int
	err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM firmware_updates WHERE tenant=? AND id=?)`, tenant, u.ID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists != 0 {
		return errors.New("firmware update already exists")
	}
	return apiv1.ErrUpdateInProgress
}
func (s *FirmwareUpdates) Get(tenant, id string) (apiv1.FirmwareUpdate, bool) {
	var b []byte
	err := s.db.QueryRow(`SELECT payload FROM firmware_updates WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return apiv1.FirmwareUpdate{}, false
	}
	failFirmwareUpdate(err)
	return firmwareUpdateDecode(b), true
}
func (s *FirmwareUpdates) ListForDevice(tenant, device string) []apiv1.FirmwareUpdate {
	rows, err := s.db.Query(`SELECT payload FROM firmware_updates WHERE tenant=? AND device=?`, tenant, device)
	failFirmwareUpdate(err)
	defer rows.Close()
	out := []apiv1.FirmwareUpdate{}
	for rows.Next() {
		var b []byte
		failFirmwareUpdate(rows.Scan(&b))
		out = append(out, firmwareUpdateDecode(b))
	}
	failFirmwareUpdate(rows.Err())
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}
func (s *FirmwareUpdates) update(tenant, id, status string, observed *apiv1.FirmwareUpdateObservation) bool {
	tx, err := s.db.Begin()
	failFirmwareUpdate(err)
	defer tx.Rollback()
	// Acquire the writer lock before reading so concurrent connections cannot
	// both hold snapshots that SQLite cannot upgrade to write transactions.
	_, err = tx.Exec(`UPDATE firmware_updates SET status=status WHERE tenant=? AND id=?`, tenant, id)
	failFirmwareUpdate(err)
	var b []byte
	err = tx.QueryRow(`SELECT payload FROM firmware_updates WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return false
	}
	failFirmwareUpdate(err)
	u := firmwareUpdateDecode(b)
	if observed == nil {
		if !apiv1.ValidFirmwareUpdateTransition(u.Status, status) {
			return false
		}
	} else {
		if apiv1.TerminalFirmwareUpdate(u.Status) || (status != u.Status && !apiv1.ValidFirmwareUpdateTransition(u.Status, status)) || (observed.Method != "auto" && observed.Method != "push" && observed.Method != "pull") {
			return false
		}
		u.Method = observed.Method
		u.URI = observed.URI
		u.DeviceState = observed.DeviceState
		u.UpdateResult = observed.UpdateResult
		u.Progress = observed.Progress
		u.Outcome = observed.Outcome
	}
	u.Status = status
	if apiv1.TerminalFirmwareUpdate(status) {
		now := time.Now().UTC()
		u.CompletedAt = &now
	}
	_, err = tx.Exec(`UPDATE firmware_updates SET status=?,payload=? WHERE tenant=? AND id=?`, status, firmwareUpdateEncode(u), tenant, id)
	failFirmwareUpdate(err)
	failFirmwareUpdate(tx.Commit())
	return true
}
func (s *FirmwareUpdates) UpdateStatus(tenant, id, status string) bool {
	return s.update(tenant, id, status, nil)
}
func (s *FirmwareUpdates) UpdateObserved(tenant, id, status string, obs apiv1.FirmwareUpdateObservation) bool {
	return s.update(tenant, id, status, &obs)
}
