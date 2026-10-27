package sqlite

import (
	"database/sql"
	"encoding/json"

	"github.com/fiumaralabs/lwm2m/store"
)

// Settings persists tenant configuration and its strong version together.
type Settings struct{ db *sql.DB }

func (s *Devices) Settings() *Settings { return &Settings{db: s.db} }

type settingsEnvelope struct {
	Version uint64              `json:"version"`
	Value   store.SettingsValue `json:"value"`
}

func readSettings(q interface{ QueryRow(string, ...any) *sql.Row }, tenant string) (store.SettingsValue, error) {
	var body []byte
	err := q.QueryRow("SELECT body FROM settings WHERE tenant=?", tenant).Scan(&body)
	if err == sql.ErrNoRows {
		return store.DefaultSettings(), nil
	}
	if err != nil {
		return store.SettingsValue{}, err
	}
	var doc settingsEnvelope
	if err := json.Unmarshal(body, &doc); err != nil {
		return store.SettingsValue{}, err
	}
	doc.Value.Version = doc.Version
	return doc.Value, nil
}

func (s *Settings) Get(tenant string) (store.SettingsValue, error) { return readSettings(s.db, tenant) }

func (s *Settings) Patch(tenant string, patch []byte, match store.WebhookMatch) (store.SettingsValue, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return store.SettingsValue{}, err
	}
	defer tx.Rollback()
	v, err := readSettings(tx, tenant)
	if err != nil {
		return store.SettingsValue{}, err
	}
	out, err := store.ApplySettingsPatch(v, patch, match)
	if err != nil {
		return store.SettingsValue{}, err
	}
	body, err := json.Marshal(settingsEnvelope{Version: out.Version, Value: out})
	if err != nil {
		return store.SettingsValue{}, err
	}
	if _, err = tx.Exec("INSERT INTO settings(tenant,body) VALUES(?,?) ON CONFLICT(tenant) DO UPDATE SET body=excluded.body", tenant, body); err != nil {
		return store.SettingsValue{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.SettingsValue{}, err
	}
	return out, nil
}

var _ store.Settings = (*Settings)(nil)
