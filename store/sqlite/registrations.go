package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"sync"

	"github.com/fiumaralabs/lwm2m/server"
)

// Registrations persists registrations. Store methods panic on storage errors:
// server.Store has no error result, and silently acknowledging a failed write
// would violate its contract. AddTx instead returns errors to its caller.
type Registrations struct {
	db   *sql.DB
	mu   sync.Mutex
	live map[string]liveRegistration

	reconcileMu sync.Mutex
	reconciling bool
	dirty       bool
}
type liveRegistration struct {
	token string
	r     *server.Registration
}
type registrationRecord struct {
	Registration            server.Registration
	AddressNetwork, Address string
	Certificate             []byte
	Token                   string
}

// Registrations returns the registration store associated with this database.
func (s *Devices) Registrations() *Registrations { return s.registrations }

func encodeRegistration(r *server.Registration) ([]byte, string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, "", err
	}
	record := registrationRecord{Registration: *r, Token: hex.EncodeToString(token[:])}
	if r.Addr != nil {
		record.AddressNetwork = r.Addr.Network()
		record.Address = r.Addr.String()
	}
	record.Registration.Addr = nil
	if r.Identity.Cert != nil {
		record.Certificate = r.Identity.Cert.Raw
		record.Registration.Identity.Cert = nil
	}
	b, err := json.Marshal(record)
	return b, record.Token, err
}
func decodeRegistration(b []byte) (*server.Registration, string, error) {
	var record registrationRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return nil, "", err
	}
	if record.Address != "" {
		record.Registration.Addr = storedAddr{record.AddressNetwork, record.Address}
	}
	if len(record.Certificate) > 0 {
		cert, err := x509.ParseCertificate(record.Certificate)
		if err != nil {
			return nil, "", err
		}
		record.Registration.Identity.Cert = cert
	}
	return &record.Registration, record.Token, nil
}

type storedAddr struct{ network, address string }

func (a storedAddr) Network() string { return a.network }
func (a storedAddr) String() string  { return a.address }

var _ net.Addr = storedAddr{}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func (s *Registrations) read(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, arg any) (*server.Registration, string, error) {
	var b []byte
	err := q.QueryRowContext(ctx, query, arg).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return decodeRegistration(b)
}
func (s *Registrations) cached(r *server.Registration, token string) *server.Registration {
	if r == nil {
		return nil
	}
	if live, ok := s.live[r.ID]; ok && live.token == token {
		return live.r
	}
	return r
}

// AddTx joins the caller's transaction. It never commits or rolls back it.
// The returned replacement reflects the database state inside that transaction.
func (s *Registrations) AddTx(ctx context.Context, tx *sql.Tx, r *server.Registration) (*server.Registration, error) {
	old, _, err := s.read(ctx, tx, "SELECT payload FROM registrations WHERE endpoint=?", r.Endpoint)
	if err != nil {
		return nil, err
	}
	payload, _, err := encodeRegistration(r)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM registrations WHERE endpoint=?", r.Endpoint); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO registrations(id,endpoint,payload) VALUES(?,?,?)", r.ID, r.Endpoint, payload)
	if err == nil {
		// Reconcile after the transaction releases the single connection. Batch
		// requests into one worker so long transactions cannot spawn per-add work.
		s.scheduleReconcile()
	}
	return old, err
}

// PublishCommitted installs a peer only while its registration still exists.
// It performs no durable write and cannot undo a concurrent removal.
func (s *Registrations) PublishCommitted(r *server.Registration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, token, err := s.read(context.Background(), s.db, "SELECT payload FROM registrations WHERE id=?", r.ID)
	must(err)
	if current != nil && current.Endpoint == r.Endpoint {
		s.live[r.ID] = liveRegistration{token, r}
	}
}

func (s *Registrations) scheduleReconcile() {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	s.dirty = true
	if s.reconciling {
		return
	}
	s.reconciling = true
	go func() {
		for {
			s.reconcileMu.Lock()
			if !s.dirty {
				s.reconciling = false
				s.reconcileMu.Unlock()
				return
			}
			s.dirty = false
			s.reconcileMu.Unlock()
			s.reconcileLive()
		}
	}()
}

func (s *Registrations) reconcileLive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT payload FROM registrations")
	if err != nil { // The database may have closed while the transaction ended.
		return
	}
	defer rows.Close()
	tokens := make(map[string]string)
	for rows.Next() {
		var payload []byte
		if rows.Scan(&payload) != nil {
			return
		}
		r, token, err := decodeRegistration(payload)
		if err != nil {
			return
		}
		tokens[r.ID] = token
	}
	if rows.Err() != nil {
		return
	}
	for id, live := range s.live {
		if token, ok := tokens[id]; !ok || live.token != token {
			delete(s.live, id)
		}
	}
}
func (s *Registrations) Add(r *server.Registration) *server.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	must(err)
	defer tx.Rollback()
	old, token, err := s.read(context.Background(), tx, "SELECT payload FROM registrations WHERE endpoint=?", r.Endpoint)
	must(err)
	payload, newToken, err := encodeRegistration(r)
	must(err)
	_, err = tx.Exec("DELETE FROM registrations WHERE endpoint=?", r.Endpoint)
	must(err)
	_, err = tx.Exec("INSERT INTO registrations(id,endpoint,payload) VALUES(?,?,?)", r.ID, r.Endpoint, payload)
	must(err)
	must(tx.Commit())
	old = s.cached(old, token)
	if old != nil {
		delete(s.live, old.ID)
	}
	s.live[r.ID] = liveRegistration{newToken, r}
	return old
}
func (s *Registrations) lookup(query string, arg any) (*server.Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, token, err := s.read(context.Background(), s.db, query, arg)
	must(err)
	return s.cached(r, token), r != nil
}
func (s *Registrations) ByID(id string) (*server.Registration, bool) {
	return s.lookup("SELECT payload FROM registrations WHERE id=?", id)
}
func (s *Registrations) ByEndpoint(ep string) (*server.Registration, bool) {
	return s.lookup("SELECT payload FROM registrations WHERE endpoint=?", ep)
}
func (s *Registrations) Update(r *server.Registration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, token, err := encodeRegistration(r)
	must(err)
	res, err := s.db.Exec("UPDATE registrations SET payload=? WHERE id=?", payload, r.ID)
	must(err)
	n, err := res.RowsAffected()
	must(err)
	if n == 0 {
		return false
	}
	s.live[r.ID] = liveRegistration{token, r}
	return true
}
func (s *Registrations) Remove(id string) (*server.Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	must(err)
	defer tx.Rollback()
	r, token, err := s.read(context.Background(), tx, "SELECT payload FROM registrations WHERE id=?", id)
	must(err)
	if r == nil {
		return nil, false
	}
	_, err = tx.Exec("DELETE FROM registrations WHERE id=?", id)
	must(err)
	must(tx.Commit())
	r = s.cached(r, token)
	delete(s.live, id)
	return r, true
}
func (s *Registrations) All() []*server.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT payload FROM registrations")
	must(err)
	defer rows.Close()
	out := []*server.Registration{}
	for rows.Next() {
		var b []byte
		must(rows.Scan(&b))
		r, token, err := decodeRegistration(b)
		must(err)
		out = append(out, s.cached(r, token))
	}
	must(rows.Err())
	return out
}

var _ server.Store = (*Registrations)(nil)
