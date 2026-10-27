package sqlite

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// APIKeys persists API key hashes and metadata on the shared database.
type APIKeys struct{ db *sql.DB }

func (s *Devices) APIKeys() *APIKeys { return &APIKeys{db: s.db} }
func (s *APIKeys) Get(id string) (store.APIKey, error) {
	var body []byte
	var k store.APIKey
	err := s.db.QueryRow("SELECT body FROM api_keys WHERE id=?", id).Scan(&body)
	if err == sql.ErrNoRows {
		return k, store.ErrAPIKeyNotFound
	}
	if err == nil {
		err = json.Unmarshal(body, &k)
	}
	return k, err
}
func (s *APIKeys) Create(k store.APIKey) error {
	body, err := json.Marshal(k)
	if err != nil {
		return err
	}
	var id string
	err = s.db.QueryRow("INSERT INTO api_keys(id,tenant,body) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING RETURNING id", k.ID, k.Tenant, body).Scan(&id)
	if err == sql.ErrNoRows {
		return store.ErrAPIKeyExists
	}
	return err
}
func (s *APIKeys) List(tenant string) ([]store.KeyMetadata, error) {
	rows, err := s.db.Query("SELECT body FROM api_keys WHERE tenant=? ORDER BY id", tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.KeyMetadata, 0)
	for rows.Next() {
		var body []byte
		var k store.APIKey
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &k); err != nil {
			return nil, err
		}
		out = append(out, k.KeyMetadata)
	}
	return out, rows.Err()
}
func keyMutation(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrAPIKeyNotFound
	}
	return err
}
func (s *APIKeys) Delete(tenant, id string) error {
	return keyMutation(s.db.Exec("DELETE FROM api_keys WHERE tenant=? AND id=?", tenant, id))
}
func (s *APIKeys) Touch(id string, at time.Time) error {
	stamp, err := at.MarshalText()
	if err != nil {
		return err
	}
	return keyMutation(s.db.Exec("UPDATE api_keys SET body=json_set(body,'$.lastUsedAt',?) WHERE id=?", string(stamp), id))
}

var _ store.APIKeys = (*APIKeys)(nil)
