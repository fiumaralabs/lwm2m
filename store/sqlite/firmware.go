package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Firmware shares the database and lifetime of its owning Devices store.
// Methods without error returns panic on database failure, like Models.
type Firmware struct{ db *sql.DB }

type firmwareRecord struct {
	apiv1.Firmware
	Labels map[string]string `json:"labels"`
}

func encodeFirmware(f apiv1.Firmware) ([]byte, error) {
	return json.Marshal(firmwareRecord{Firmware: f, Labels: f.Labels})
}

var _ apiv1.FirmwareStore = (*Firmware)(nil)

func (s *Devices) Firmware() *Firmware { return &Firmware{db: s.db} }

func (s *Firmware) Create(tenant string, f apiv1.Firmware) error {
	b, err := encodeFirmware(f)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO firmware(tenant,id,payload) VALUES(?,?,?) ON CONFLICT(tenant,id) DO UPDATE SET payload=excluded.payload`, tenant, f.ID, string(b))
	return err
}
func decodeFirmware(b []byte) apiv1.Firmware {
	var record firmwareRecord
	if err := json.Unmarshal(b, &record); err != nil {
		panic(fmt.Errorf("firmware decode: %w", err))
	}
	record.Firmware.Labels = record.Labels
	return record.Firmware
}
func (s *Firmware) List(tenant string) []apiv1.Firmware {
	rows, err := s.db.Query(`SELECT payload FROM firmware WHERE tenant=? ORDER BY id`, tenant)
	if err != nil {
		panic(fmt.Errorf("firmware list: %w", err))
	}
	defer rows.Close()
	out := []apiv1.Firmware{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			panic(fmt.Errorf("firmware list: %w", err))
		}
		out = append(out, decodeFirmware(b))
	}
	if err := rows.Err(); err != nil {
		panic(fmt.Errorf("firmware list: %w", err))
	}
	return out
}
func (s *Firmware) Get(tenant, id string) (apiv1.Firmware, bool) {
	var b []byte
	err := s.db.QueryRow(`SELECT payload FROM firmware WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return apiv1.Firmware{}, false
	}
	if err != nil {
		panic(fmt.Errorf("firmware get: %w", err))
	}
	return decodeFirmware(b), true
}
func (s *Firmware) Patch(tenant, id string, p apiv1.FirmwarePatch) (apiv1.Firmware, bool) {
	tx, err := s.db.Begin()
	if err != nil {
		panic(fmt.Errorf("firmware patch: %w", err))
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRow(`SELECT payload FROM firmware WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err == sql.ErrNoRows {
		return apiv1.Firmware{}, false
	}
	if err != nil {
		panic(fmt.Errorf("firmware patch: %w", err))
	}
	f := decodeFirmware(b)
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.Description != nil {
		f.Description = *p.Description
	}
	if p.Labels != nil {
		f.Labels = *p.Labels
	}
	b, err = encodeFirmware(f)
	if err != nil {
		panic(fmt.Errorf("firmware patch: %w", err))
	}
	if _, err = tx.Exec(`UPDATE firmware SET payload=? WHERE tenant=? AND id=?`, string(b), tenant, id); err != nil {
		panic(fmt.Errorf("firmware patch: %w", err))
	}
	if err = tx.Commit(); err != nil {
		panic(fmt.Errorf("firmware patch: %w", err))
	}
	return decodeFirmware(b), true
}
func (s *Firmware) Delete(tenant, id string) (bool, bool) {
	result, err := s.db.Exec(`DELETE FROM firmware WHERE tenant=? AND id=?`, tenant, id)
	if err != nil {
		panic(fmt.Errorf("firmware delete: %w", err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("firmware delete: %w", err))
	}
	return n != 0, false
}
