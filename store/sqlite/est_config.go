package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// ESTConfigs shares the Devices connection and lifetime. Database failures panic
// because ESTConfigStore cannot return errors.
type ESTConfigs struct{ db *sql.DB }

var _ apiv1.ESTConfigStore = (*ESTConfigs)(nil)

func (s *Devices) ESTConfigs() *ESTConfigs { return &ESTConfigs{db: s.db} }

func (s *ESTConfigs) Get(tenant string) (apiv1.ESTConfig, bool) {
	var c apiv1.ESTConfig
	err := s.db.QueryRow(`SELECT enabled, certificate, private_key, validity_days, selector FROM est_configs WHERE tenant=?`, tenant).Scan(&c.Enabled, &c.Certificate, &c.PrivateKey, &c.ValidityDays, &c.Selector)
	if err == sql.ErrNoRows {
		return apiv1.ESTConfig{}, false
	}
	if err != nil {
		panic(fmt.Errorf("est config get: %w", err))
	}
	return c, true
}

func (s *ESTConfigs) Put(tenant string, c apiv1.ESTConfig) bool {
	tx, err := s.db.Begin()
	if err != nil {
		panic(fmt.Errorf("est config put: %w", err))
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO est_configs (tenant, enabled, certificate, private_key, validity_days, selector) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(tenant) DO NOTHING`, tenant, c.Enabled, c.Certificate, c.PrivateKey, c.ValidityDays, c.Selector)
	if err != nil {
		panic(fmt.Errorf("est config put: %w", err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("est config put: %w", err))
	}
	if n == 0 {
		_, err = tx.Exec(`UPDATE est_configs SET enabled=?, certificate=?, private_key=?, validity_days=?, selector=? WHERE tenant=?`, c.Enabled, c.Certificate, c.PrivateKey, c.ValidityDays, c.Selector, tenant)
		if err != nil {
			panic(fmt.Errorf("est config put: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		panic(fmt.Errorf("est config put: %w", err))
	}
	return n == 0
}
