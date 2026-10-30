package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// DeviceProfiles shares the database and lifetime of its owning Devices store.
// Methods without an error return panic on database failures, like Firmware.
type DeviceProfiles struct{ db *sql.DB }

var _ apiv1.DeviceProfileStore = (*DeviceProfiles)(nil)

func (s *Devices) DeviceProfiles() *DeviceProfiles { return &DeviceProfiles{db: s.db} }

func (s *DeviceProfiles) Create(tenant string, p apiv1.DeviceProfile) error {
	_, err := s.db.Exec(`INSERT INTO device_profiles(tenant,id,name,observations,attributes,on_register,timeseries,bootstrap_template) VALUES(?,?,?,?,?,?,?,?)`, tenant, p.ID, p.Name, []byte(p.Observations), []byte(p.Attributes), []byte(p.OnRegister), []byte(p.Timeseries), []byte(p.BootstrapTemplate))
	return err
}

// Replay and CreateIdempotent remain available to direct store consumers.
func (s *DeviceProfiles) Replay(tenant, key string) (apiv1.DeviceProfile, bool) {
	r, ok := s.ReplayResult(tenant, key)
	if !ok || r.Profile == nil {
		return apiv1.DeviceProfile{}, false
	}
	return *r.Profile, true
}
func (s *DeviceProfiles) CreateIdempotent(tenant string, p apiv1.DeviceProfile, key string) (apiv1.DeviceProfile, error) {
	r, err := s.CreateResult(tenant, key, apiv1.ProfileCreateResult{Profile: &p})
	if err != nil {
		return apiv1.DeviceProfile{}, err
	}
	if r.Profile == nil {
		return apiv1.DeviceProfile{}, fmt.Errorf("idempotency key contains an error response")
	}
	return *r.Profile, nil
}
func decodeProfileResult(b []byte) (apiv1.ProfileCreateResult, error) {
	var r apiv1.ProfileCreateResult
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	// The original migration stored the profile directly; keep existing replay records readable.
	if r.Profile == nil && r.Problem == nil {
		var p apiv1.DeviceProfile
		if err := json.Unmarshal(b, &p); err != nil {
			return r, err
		}
		r.Profile = &p
	}
	return r, nil
}
func (s *DeviceProfiles) ReplayResult(tenant, key string) (apiv1.ProfileCreateResult, bool) {
	var b []byte
	if key == "" {
		return apiv1.ProfileCreateResult{}, false
	}
	err := s.db.QueryRow(`SELECT body FROM device_profile_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, time.Now().UnixNano()).Scan(&b)
	if err == sql.ErrNoRows {
		return apiv1.ProfileCreateResult{}, false
	}
	if err != nil {
		panic(fmt.Errorf("device profile replay: %w", err))
	}
	r, err := decodeProfileResult(b)
	if err != nil {
		panic(fmt.Errorf("device profile replay: %w", err))
	}
	return r, true
}
func (s *DeviceProfiles) CreateResult(tenant, key string, result apiv1.ProfileCreateResult) (apiv1.ProfileCreateResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return apiv1.ProfileCreateResult{}, err
	}
	defer tx.Rollback()
	now := time.Now()
	if key != "" {
		var b []byte
		err = tx.QueryRow(`SELECT body FROM device_profile_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, now.UnixNano()).Scan(&b)
		if err == nil {
			return decodeProfileResult(b)
		}
		if err != sql.ErrNoRows {
			return apiv1.ProfileCreateResult{}, err
		}
	}
	if result.Profile != nil {
		p := result.Profile
		_, err = tx.Exec(`INSERT INTO device_profiles(tenant,id,name,observations,attributes,on_register,timeseries,bootstrap_template) VALUES(?,?,?,?,?,?,?,?)`, tenant, p.ID, p.Name, []byte(p.Observations), []byte(p.Attributes), []byte(p.OnRegister), []byte(p.Timeseries), []byte(p.BootstrapTemplate))
		if err != nil {
			return apiv1.ProfileCreateResult{}, err
		}
	}
	if _, err = tx.Exec(`DELETE FROM device_profile_replays WHERE expires<=?`, now.UnixNano()); err != nil {
		return apiv1.ProfileCreateResult{}, err
	}
	if key != "" {
		b, e := json.Marshal(result)
		if e != nil {
			return apiv1.ProfileCreateResult{}, e
		}
		if _, err = tx.Exec(`INSERT INTO device_profile_replays(tenant,key,body,expires) VALUES(?,?,?,?)`, tenant, key, b, now.Add(24*time.Hour).UnixNano()); err != nil {
			return apiv1.ProfileCreateResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return apiv1.ProfileCreateResult{}, err
	}
	return result, nil
}

type profileScanner interface{ Scan(...any) error }

func scanDeviceProfile(row profileScanner) (apiv1.DeviceProfile, error) {
	var p apiv1.DeviceProfile
	var observations, attributes, onRegister, timeseries, bootstrapTemplate []byte
	err := row.Scan(&p.ID, &p.Name, &observations, &attributes, &onRegister, &timeseries, &bootstrapTemplate)
	p.Observations, p.Attributes, p.OnRegister = observations, attributes, onRegister
	p.Timeseries, p.BootstrapTemplate = timeseries, bootstrapTemplate
	return p, err
}

const profileColumns = `id,name,observations,attributes,on_register,timeseries,bootstrap_template`

func (s *DeviceProfiles) Get(tenant, id string) (apiv1.DeviceProfile, bool) {
	p, err := scanDeviceProfile(s.db.QueryRow(`SELECT `+profileColumns+` FROM device_profiles WHERE tenant=? AND id=?`, tenant, id))
	if err == sql.ErrNoRows {
		return apiv1.DeviceProfile{}, false
	}
	if err != nil {
		panic(fmt.Errorf("device profile get: %w", err))
	}
	return p, true
}
func (s *DeviceProfiles) List(tenant string) []apiv1.DeviceProfile {
	rows, err := s.db.Query(`SELECT `+profileColumns+` FROM device_profiles WHERE tenant=? ORDER BY id`, tenant)
	if err != nil {
		panic(fmt.Errorf("device profile list: %w", err))
	}
	defer rows.Close()
	out := []apiv1.DeviceProfile{}
	for rows.Next() {
		p, err := scanDeviceProfile(rows)
		if err != nil {
			panic(fmt.Errorf("device profile list: %w", err))
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		panic(fmt.Errorf("device profile list: %w", err))
	}
	return out
}
func (s *DeviceProfiles) Replace(tenant string, p apiv1.DeviceProfile) bool {
	result, err := s.db.Exec(`UPDATE device_profiles SET name=?,observations=?,attributes=?,on_register=?,timeseries=?,bootstrap_template=? WHERE tenant=? AND id=?`, p.Name, []byte(p.Observations), []byte(p.Attributes), []byte(p.OnRegister), []byte(p.Timeseries), []byte(p.BootstrapTemplate), tenant, p.ID)
	return profileAffected(result, err, "replace")
}
func (s *DeviceProfiles) Delete(tenant, id string) (bool, bool) {
	result, err := s.db.Exec(`DELETE FROM device_profiles WHERE tenant=? AND id=?`, tenant, id)
	return profileAffected(result, err, "delete"), false
}
func profileAffected(result sql.Result, err error, op string) bool {
	if err != nil {
		panic(fmt.Errorf("device profile %s: %w", op, err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("device profile %s: %w", op, err))
	}
	return n != 0
}
