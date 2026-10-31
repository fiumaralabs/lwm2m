package sqlite

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store"
)

type TrustAnchors struct {
	devices *Devices
	limit   int
}

func (d *Devices) TrustAnchors(limit int) *TrustAnchors {
	if limit <= 0 {
		limit = 1000
	}
	return &TrustAnchors{d, limit}
}

var _ apiv1.TrustAnchorStore = (*TrustAnchors)(nil)

func encodeAnchor(v any) ([]byte, error) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}
func decodeAnchor(b []byte, v any) error { return gob.NewDecoder(bytes.NewReader(b)).Decode(v) }

func (s *TrustAnchors) create(tx *sql.Tx, tenant string, a apiv1.TrustAnchor) error {
	// Take the write lock before checking uniqueness or the global limit.
	if _, err := tx.Exec(`UPDATE trust_anchors SET id=id WHERE 0`); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM trust_anchors WHERE tenant=? AND id=?`, tenant, a.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return apiv1.ErrTrustAnchorDuplicate
	}
	for _, c := range a.Certificates {
		if err := tx.QueryRow(`SELECT count(*) FROM trust_anchor_fingerprints WHERE tenant=? AND fingerprint=?`, tenant, c.Fingerprint).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return apiv1.ErrTrustAnchorDuplicate
		}
	}
	if err := tx.QueryRow(`SELECT count(*) FROM trust_anchors`).Scan(&n); err != nil {
		return err
	}
	if n >= s.limit {
		return apiv1.ErrTrustAnchorLimit
	}
	payload, err := encodeAnchor(a)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO trust_anchors VALUES(?,?,?)`, tenant, a.ID, payload); err != nil {
		return err
	}
	for _, c := range a.Certificates {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO trust_anchor_fingerprints VALUES(?,?,?)`, tenant, c.Fingerprint, a.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *TrustAnchors) Create(tenant string, a apiv1.TrustAnchor) error {
	tx, err := s.devices.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.create(tx, tenant, a); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *TrustAnchors) List(tenant string) []apiv1.TrustAnchor {
	out := make([]apiv1.TrustAnchor, 0)
	rows, err := s.devices.db.Query(`SELECT payload FROM trust_anchors WHERE tenant=? ORDER BY id`, tenant)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var a apiv1.TrustAnchor
		if rows.Scan(&b) != nil || decodeAnchor(b, &a) != nil {
			return nil
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}
func (s *TrustAnchors) Get(tenant, id string) (apiv1.TrustAnchor, bool) {
	var a apiv1.TrustAnchor
	var b []byte
	err := s.devices.db.QueryRow(`SELECT payload FROM trust_anchors WHERE tenant=? AND id=?`, tenant, id).Scan(&b)
	if err != nil || decodeAnchor(b, &a) != nil {
		return apiv1.TrustAnchor{}, false
	}
	return a, true
}
func (s *TrustAnchors) Delete(tenant, id string) bool {
	found, matched := s.DeleteMatch(tenant, id, store.WebhookMatch{})
	return found && matched
}
func (s *TrustAnchors) DeleteMatch(tenant, id string, match store.WebhookMatch) (bool, bool) {
	tx, err := s.devices.db.Begin()
	if err != nil {
		return false, false
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE trust_anchors SET id=id WHERE 0`); err != nil {
		return false, false
	}
	var b []byte
	var a apiv1.TrustAnchor
	if tx.QueryRow(`SELECT payload FROM trust_anchors WHERE tenant=? AND id=?`, tenant, id).Scan(&b) != nil || decodeAnchor(b, &a) != nil {
		return false, false
	}
	payload, _ := json.Marshal(a)
	sum := sha256.Sum256(payload)
	tag := fmt.Sprintf("\"anchor-v1-%x\"", sum)
	matched := !match.Required || match.Any
	for _, v := range match.Tags {
		if v == tag {
			matched = true
		}
	}
	if !matched {
		return true, false
	}
	if _, err = tx.Exec(`DELETE FROM trust_anchor_fingerprints WHERE tenant=? AND anchor_id=?`, tenant, id); err != nil {
		return true, false
	}
	if _, err = tx.Exec(`DELETE FROM trust_anchors WHERE tenant=? AND id=?`, tenant, id); err != nil {
		return true, false
	}
	return true, tx.Commit() == nil
}
func (s *TrustAnchors) Replay(tenant, key string) (apiv1.TrustAnchorResult, bool) {
	var r apiv1.TrustAnchorResult
	var b []byte
	if key == "" || s.devices.db.QueryRow(`SELECT payload FROM trust_anchor_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, time.Now().UnixNano()).Scan(&b) != nil || decodeAnchor(b, &r) != nil {
		return apiv1.TrustAnchorResult{}, false
	}
	return r, true
}
func (s *TrustAnchors) CreateResult(tenant, key string, r apiv1.TrustAnchorResult) apiv1.TrustAnchorResult {
	capacity := func(detail string) apiv1.TrustAnchorResult {
		return apiv1.TrustAnchorResult{Problem: &apiv1.Problem{Status: 503, Code: "capacity_exceeded", Detail: detail}}
	}
	tx, err := s.devices.db.Begin()
	if err != nil {
		return capacity("Trust anchor capacity reached")
	}
	defer tx.Rollback()
	now := time.Now()
	if key != "" {
		if _, err = tx.Exec(`DELETE FROM trust_anchor_replays WHERE expires<=?`, now.UnixNano()); err != nil {
			return capacity("Replay capacity reached")
		}
		var b []byte
		err = tx.QueryRow(`SELECT payload FROM trust_anchor_replays WHERE tenant=? AND key=?`, tenant, key).Scan(&b)
		if err == nil {
			var existing apiv1.TrustAnchorResult
			if decodeAnchor(b, &existing) == nil {
				return existing
			}
			return capacity("Replay capacity reached")
		}
		if err != sql.ErrNoRows {
			return capacity("Replay capacity reached")
		}
		var n int
		if tx.QueryRow(`SELECT count(*) FROM trust_anchor_replays`).Scan(&n) != nil || n >= 10000 {
			return capacity("Replay capacity reached")
		}
	}
	if r.Problem == nil && r.Anchor != nil {
		a := *r.Anchor
		a.Manual, a.ESTIssuer = true, false
		r.Anchor = &a
		if err = s.create(tx, tenant, *r.Anchor); err != nil {
			r.Anchor = nil
			if errors.Is(err, apiv1.ErrTrustAnchorDuplicate) {
				r.Problem = &apiv1.Problem{Status: 409, Code: "duplicate_trust_anchor", Detail: "Certificate already exists"}
			} else if errors.Is(err, apiv1.ErrTrustAnchorLimit) {
				r = capacity("Trust anchor capacity reached")
			} else {
				return capacity("Trust anchor capacity reached")
			}
		}
	}
	if key != "" {
		snapshot := r
		if r.Anchor != nil {
			a := *r.Anchor
			a.PEM = ""
			snapshot.Anchor = &a
		}
		b, e := encodeAnchor(snapshot)
		if e != nil {
			return capacity("Replay capacity reached")
		}
		if _, e = tx.Exec(`INSERT INTO trust_anchor_replays VALUES(?,?,?,?)`, tenant, key, b, now.Add(24*time.Hour).UnixNano()); e != nil {
			return capacity("Replay capacity reached")
		}
	}
	if tx.Commit() != nil {
		return capacity("Trust anchor capacity reached")
	}
	if r.Anchor != nil {
		a := *r.Anchor
		a.Certificates = append([]apiv1.TrustAnchorCertificate(nil), a.Certificates...)
		r.Anchor = &a
	}
	if r.Problem != nil {
		p := *r.Problem
		r.Problem = &p
	}
	return r
}
