package sqlite

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// BootstrapCredentials shares the Devices connection and lifetime. Database failures
// panic because BootstrapCredentialStore has no error return.
type BootstrapCredentials struct{ db *sql.DB }

var _ apiv1.BootstrapCredentialStore = (*BootstrapCredentials)(nil)

func (s *Devices) BootstrapCredentials() apiv1.BootstrapCredentialStore {
	return &BootstrapCredentials{db: s.db}
}

func (s *BootstrapCredentials) Get(tenant, endpoint string) (apiv1.BootstrapCredential, bool) {
	var c apiv1.BootstrapCredential
	var updated string
	err := s.db.QueryRow(`SELECT identity, key, updated_at FROM bootstrap_credentials WHERE tenant=? AND endpoint=?`, tenant, endpoint).Scan(&c.Identity, &c.Key, &updated)
	if err == sql.ErrNoRows {
		return apiv1.BootstrapCredential{}, false
	}
	if err != nil {
		panic(fmt.Errorf("bootstrap credential get: %w", err))
	}
	c.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		panic(fmt.Errorf("bootstrap credential timestamp: %w", err))
	}
	return c, true
}

func (s *BootstrapCredentials) Put(tenant, endpoint string, c apiv1.BootstrapCredential) (bool, bool) {
	tx, err := s.db.Begin()
	if err != nil {
		panic(fmt.Errorf("bootstrap credential put: %w", err))
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.Exec(`INSERT OR IGNORE INTO bootstrap_credentials (tenant, endpoint, identity, key, updated_at) VALUES (?, ?, ?, ?, ?)`, tenant, endpoint, c.Identity, c.Key, now)
	if err != nil {
		panic(fmt.Errorf("bootstrap credential put: %w", err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("bootstrap credential put: %w", err))
	}
	if n == 0 {
		var conflict int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM bootstrap_credentials WHERE identity=? AND (tenant<>? OR endpoint<>?))`, c.Identity, tenant, endpoint).Scan(&conflict)
		if err != nil {
			panic(fmt.Errorf("bootstrap credential put: %w", err))
		}
		if conflict != 0 {
			return false, true
		}
		_, err = tx.Exec(`UPDATE bootstrap_credentials SET identity=?, key=?, updated_at=? WHERE tenant=? AND endpoint=?`, c.Identity, c.Key, now, tenant, endpoint)
		if err != nil {
			panic(fmt.Errorf("bootstrap credential put: %w", err))
		}
	}
	if err = tx.Commit(); err != nil {
		panic(fmt.Errorf("bootstrap credential put: %w", err))
	}
	return n == 0, false
}

func (s *BootstrapCredentials) Delete(tenant, endpoint string) bool {
	result, err := s.db.Exec(`DELETE FROM bootstrap_credentials WHERE tenant=? AND endpoint=?`, tenant, endpoint)
	if err != nil {
		panic(fmt.Errorf("bootstrap credential delete: %w", err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("bootstrap credential delete: %w", err))
	}
	return n != 0
}
