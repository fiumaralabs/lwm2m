package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Tenants shares the owning Devices connection and lifetime. Database errors
// panic because TenantStore cannot return errors.
type Tenants struct{ db *sql.DB }

var _ apiv1.TenantStore = (*Tenants)(nil)

func (s *Devices) Tenants() *Tenants { return &Tenants{db: s.db} }

func tenantDecode(payload string) apiv1.Tenant {
	var t apiv1.Tenant
	if err := json.Unmarshal([]byte(payload), &t); err != nil {
		panic(fmt.Errorf("tenants decode: %w", err))
	}
	return t
}
func tenantEncode(t apiv1.Tenant) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(fmt.Errorf("tenants encode: %w", err))
	}
	return string(b)
}
func (s *Tenants) Get(id string) (apiv1.Tenant, bool) {
	var payload string
	err := s.db.QueryRow("SELECT payload FROM tenants WHERE store_key=?", id).Scan(&payload)
	if err == sql.ErrNoRows {
		return apiv1.Tenant{}, false
	}
	if err != nil {
		panic(fmt.Errorf("tenants get: %w", err))
	}
	return tenantDecode(payload), true
}
func (s *Tenants) List() []apiv1.Tenant {
	rows, err := s.db.Query("SELECT payload FROM tenants")
	if err != nil {
		panic(fmt.Errorf("tenants list: %w", err))
	}
	defer rows.Close()
	result := make([]apiv1.Tenant, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			panic(fmt.Errorf("tenants scan: %w", err))
		}
		result = append(result, tenantDecode(payload))
	}
	if err := rows.Err(); err != nil {
		panic(fmt.Errorf("tenants list: %w", err))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (s *Tenants) Create(t apiv1.Tenant) bool {
	r, err := s.db.Exec("INSERT INTO tenants(store_key,payload) VALUES(?,?) ON CONFLICT(store_key) DO NOTHING", t.ID, tenantEncode(t))
	if err != nil {
		panic(fmt.Errorf("tenants create: %w", err))
	}
	n, err := r.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("tenants create: %w", err))
	}
	return n != 0
}
func (s *Tenants) Update(id string, f func(*apiv1.Tenant) bool) (apiv1.Tenant, bool, bool) {
	tx, err := s.db.Begin()
	if err != nil {
		panic(fmt.Errorf("tenants update: %w", err))
	}
	defer tx.Rollback()
	var payload string
	err = tx.QueryRow("SELECT payload FROM tenants WHERE store_key=?", id).Scan(&payload)
	if err == sql.ErrNoRows {
		return apiv1.Tenant{}, false, false
	}
	if err != nil {
		panic(fmt.Errorf("tenants update: %w", err))
	}
	t := tenantDecode(payload)
	if !f(&t) {
		return t, true, false
	}
	if _, err = tx.Exec("UPDATE tenants SET payload=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE store_key=?", tenantEncode(t), id); err != nil {
		panic(fmt.Errorf("tenants update: %w", err))
	}
	if err = tx.Commit(); err != nil {
		panic(fmt.Errorf("tenants update: %w", err))
	}
	return t, true, true
}
func (s *Tenants) Delete(id string) bool {
	r, err := s.db.Exec("DELETE FROM tenants WHERE store_key=?", id)
	if err != nil {
		panic(fmt.Errorf("tenants delete: %w", err))
	}
	n, err := r.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("tenants delete: %w", err))
	}
	return n != 0
}
