package sqlite

import (
	"database/sql"
	"fmt"

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
