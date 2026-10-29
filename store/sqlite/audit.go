package sqlite

import (
	"database/sql"
	"encoding/json"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// Audit shares its owning Devices store's connection and lifetime.
type Audit struct{ db *sql.DB }

var _ apiv1.AuditStore = (*Audit)(nil)

func (s *Devices) Audit() *Audit { return &Audit{db: s.db} }
func (s *Audit) Append(tenant string, entry apiv1.AuditEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO audit_log(tenant,body) VALUES(?,?)", tenant, string(body))
	return err
}
func (s *Audit) List(tenant string) ([]apiv1.AuditEntry, error) {
	rows, err := s.db.Query("SELECT body FROM audit_log WHERE tenant=? ORDER BY sequence", tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []apiv1.AuditEntry
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var entry apiv1.AuditEntry
		if err := json.Unmarshal([]byte(body), &entry); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
