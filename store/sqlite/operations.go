package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

// Operations shares its owning Devices store's connection and lifetime.
type Operations struct{ db *sql.DB }

var _ operations.Operations = (*Operations)(nil)
var _ operations.Recoverer = (*Operations)(nil)

func (s *Devices) Operations() *Operations { return &Operations{db: s.db} }

func (s *Operations) Create(ctx context.Context, tenant string, o operations.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant != o.Tenant {
		return store.ErrTenant
	}
	body, err := json.Marshal(o)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO operations(tenant,id,device,status,seq,body) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant,id) DO NOTHING", tenant, o.ID, o.Device, o.Status, o.CreatedAt.UnixNano(), string(body))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrExists
	}
	return err
}

// scanOperation restores Tenant, which is deliberately excluded from JSON.
func scanOperation(row interface{ Scan(...any) error }) (operations.Operation, error) {
	var o operations.Operation
	var tenant, body string
	err := row.Scan(&tenant, &body)
	if err == sql.ErrNoRows {
		return o, store.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(body), &o)
		o.Tenant = tenant
	}
	return o, err
}

func (s *Operations) Get(ctx context.Context, tenant, id string) (operations.Operation, error) {
	return scanOperation(s.db.QueryRowContext(ctx, "SELECT tenant,body FROM operations WHERE tenant=? AND id=?", tenant, id))
}

func (s *Operations) Update(ctx context.Context, tenant, id string, expected operations.Status, change operations.Change) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	o, err := scanOperation(tx.QueryRowContext(ctx, "SELECT tenant,body FROM operations WHERE tenant=? AND id=?", tenant, id))
	if err != nil {
		return err
	}
	if o.Status != expected {
		return operations.ErrTransition
	}
	if err := o.Apply(change); err != nil {
		return err
	}
	body, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE operations SET status=?,body=? WHERE tenant=? AND id=?", o.Status, string(body), tenant, id); err != nil {
		return err
	}
	return tx.Commit()
}

// read sorts decoded timestamps rather than JSON text, preserving time-zone and
// subsecond ordering exactly as the memory store does.
func (s *Operations) read(ctx context.Context, where string, ascending bool, args ...any) ([]operations.Operation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tenant,body FROM operations WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []operations.Operation{}
	for rows.Next() {
		o, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !ascending {
			a, b = b, a
		}
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out, nil
}

func (s *Operations) Pending(ctx context.Context, tenant, device string) ([]operations.Operation, error) {
	return s.read(ctx, "tenant=? AND (?='' OR device=?) AND status=?", true, tenant, device, device, operations.Queued)
}

// Unfinished includes queued and sent operations across every tenant.
func (s *Operations) Unfinished(ctx context.Context) ([]operations.Operation, error) {
	return s.read(ctx, "status IN (?,?)", true, operations.Queued, operations.Sent)
}

type operationCursor struct {
	Tenant, Filter, ID string
	At                 time.Time
}

func (s *Operations) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	f.Status = slices.Clone(f.Status)
	slices.Sort(f.Status)
	f.Status = slices.Compact(f.Status)
	if len(f.Status) == 0 {
		f.Status = nil
	}
	query, _ := json.Marshal(f)
	c := operationCursor{Tenant: tenant, Filter: string(query)}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.Filter != string(query) || c.ID == "" {
			return nil, "", store.ErrCursor
		}
	}
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrCursor
	}
	items, err := s.read(ctx, "tenant=? AND (?='' OR device=?)", false, tenant, f.Device, f.Device)
	if err != nil {
		return nil, "", err
	}
	out := make([]operations.Operation, 0)
	for _, o := range items {
		if (f.Type != "" && o.Type != f.Type) || (f.Job != "" && (o.Job == nil || *o.Job != f.Job)) || (len(f.Status) != 0 && !slices.Contains(f.Status, o.Status)) {
			continue
		}
		if c.ID != "" && (o.CreatedAt.After(c.At) || (o.CreatedAt.Equal(c.At) && o.ID >= c.ID)) {
			continue
		}
		if len(out) == limit {
			last := out[len(out)-1]
			c.ID, c.At = last.ID, last.CreatedAt
			b, _ := json.Marshal(c)
			return out, base64.RawURLEncoding.EncodeToString(b), nil
		}
		out = append(out, o)
	}
	return out, "", nil
}
