package sqlite

import (
	"database/sql"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

var _ apiv1.ESTIssuerRotator = (*TrustAnchors)(nil)

// PutConfigRotatingIssuerAnchor updates both records in one write transaction.
func (s *TrustAnchors) PutConfigRotatingIssuerAnchor(tenant string, c apiv1.ESTConfig, a apiv1.TrustAnchor) (bool, error) {
	tx, err := s.devices.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE trust_anchors SET id=id WHERE 0`); err != nil {
		return false, err
	}
	var previous string
	err = tx.QueryRow(`SELECT certificate FROM est_configs WHERE tenant=?`, tenant).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	configExists := err == nil
	rows, err := tx.Query(`SELECT id,payload FROM trust_anchors WHERE tenant=?`, tenant)
	if err != nil {
		return false, err
	}
	var ids []string
	unmarked, matching := false, false
	for rows.Next() {
		var id string
		var b []byte
		var old apiv1.TrustAnchor
		if err = rows.Scan(&id, &b); err == nil {
			err = decodeAnchor(b, &old)
		}
		if err != nil {
			rows.Close()
			return false, err
		}
		if old.ESTIssuer {
			ids = append(ids, id)
			matching = matching || old.PEM == previous
		} else {
			unmarked = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if unmarked && (!configExists || !matching) {
		return false, apiv1.ErrESTIssuerOwnershipAmbiguous
	}
	for _, id := range ids {
		if _, err = tx.Exec(`DELETE FROM trust_anchor_fingerprints WHERE tenant=? AND anchor_id=?`, tenant, id); err != nil {
			return false, err
		}
		if _, err = tx.Exec(`DELETE FROM trust_anchors WHERE tenant=? AND id=?`, tenant, id); err != nil {
			return false, err
		}
	}
	a.ESTIssuer = true
	if err = s.create(tx, tenant, a); err != nil {
		return false, err
	}
	res, err := tx.Exec(`INSERT INTO est_configs (tenant,enabled,certificate,private_key,validity_days,selector) VALUES (?,?,?,?,?,?) ON CONFLICT(tenant) DO NOTHING`, tenant, c.Enabled, c.Certificate, c.PrivateKey, c.ValidityDays, c.Selector)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		if _, err = tx.Exec(`UPDATE est_configs SET enabled=?,certificate=?,private_key=?,validity_days=?,selector=? WHERE tenant=?`, c.Enabled, c.Certificate, c.PrivateKey, c.ValidityDays, c.Selector, tenant); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 0, nil
}
