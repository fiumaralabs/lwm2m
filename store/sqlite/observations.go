package sqlite

import (
	"database/sql"
	"encoding/json"

	"github.com/fiumaralabs/lwm2m/store"
)

// Observations shares its owning Devices store's connection and lifetime.
type Observations struct{ db *sql.DB }

var _ store.Observations = (*Observations)(nil)

func (s *Devices) Observations() *Observations { return &Observations{db: s.db} }

// Store raw JSON as bytes so exact contents and nil values survive encoding.
type observationPayload struct {
	store.Observation
	Attributes map[string][]byte `json:"attributes"`
	Error      []byte            `json:"error"`
}

func encodeObservation(v store.Observation) ([]byte, error) {
	p := observationPayload{Observation: v, Error: v.Error}
	if v.Attributes != nil {
		p.Attributes = make(map[string][]byte, len(v.Attributes))
		for k, value := range v.Attributes {
			p.Attributes[k] = value
		}
	}
	return json.Marshal(p)
}

func decodeObservation(body []byte, tenant string) (store.Observation, error) {
	var p observationPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return store.Observation{}, err
	}
	v := p.Observation
	v.Tenant, v.Error = tenant, p.Error
	if p.Attributes != nil {
		v.Attributes = make(map[string]json.RawMessage, len(p.Attributes))
		for k, value := range p.Attributes {
			v.Attributes[k] = value
		}
	}
	return v, nil
}

func (s *Observations) Create(tenant string, v store.Observation) error {
	if v.Tenant != tenant {
		return store.ErrTenant
	}
	body, err := encodeObservation(v)
	if err != nil {
		return err
	}
	result, err := s.db.Exec("INSERT INTO observations(tenant,id,device,body) VALUES(?,?,?,?) ON CONFLICT(tenant,id) DO NOTHING", tenant, v.ID, v.Device, string(body))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrExists
	}
	return err
}

func scanObservation(row interface{ Scan(...any) error }) (store.Observation, error) {
	var v store.Observation
	var tenant, body string
	err := row.Scan(&tenant, &body)
	if err == sql.ErrNoRows {
		return v, store.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	return decodeObservation([]byte(body), tenant)
}

func (s *Observations) Get(tenant, id string) (store.Observation, error) {
	return scanObservation(s.db.QueryRow("SELECT tenant,body FROM observations WHERE tenant=? AND id=?", tenant, id))
}

func (s *Observations) List(tenant, device string) ([]store.Observation, error) {
	rows, err := s.db.Query("SELECT tenant,body FROM observations WHERE tenant=? AND device=? ORDER BY id", tenant, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.Observation{}
	for rows.Next() {
		v, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// mutate serializes read-modify-write in the shared SQLite transaction.
func (s *Observations) mutate(tenant, id string, change func(*store.Observation) error) (store.Observation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return store.Observation{}, err
	}
	defer tx.Rollback()
	v, err := scanObservation(tx.QueryRow("SELECT tenant,body FROM observations WHERE tenant=? AND id=?", tenant, id))
	if err != nil {
		return v, err
	}
	if err = change(&v); err != nil {
		return store.Observation{}, err
	}
	body, err := encodeObservation(v)
	if err != nil {
		return store.Observation{}, err
	}
	if _, err = tx.Exec("UPDATE observations SET body=? WHERE tenant=? AND id=?", string(body), tenant, id); err != nil {
		return store.Observation{}, err
	}
	if err = tx.Commit(); err != nil {
		return store.Observation{}, err
	}
	// Decode again so returned values cannot alias the caller's inputs.
	return decodeObservation(body, tenant)
}

func (s *Observations) UpdateAttributes(tenant, id string, a map[string]json.RawMessage) (store.Observation, error) {
	return s.mutate(tenant, id, func(v *store.Observation) error { v.Attributes = a; return nil })
}

// UpdateDelivery atomically updates metadata without reviving ended intent.
func (s *Observations) UpdateDelivery(tenant, id string, update func(*store.Observation)) (store.Observation, error) {
	return s.mutate(tenant, id, func(v *store.Observation) error {
		if v.Status != "ended" {
			update(v)
		}
		return nil
	})
}

func (s *Observations) End(tenant, id string) error {
	_, err := s.mutate(tenant, id, func(v *store.Observation) error {
		if v.Source == "profile" {
			return store.ErrObservationProfile
		}
		v.Status = "ended"
		return nil
	})
	return err
}
