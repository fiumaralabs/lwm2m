package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type pendingRegister struct {
	id string
	at time.Time
}

type admittingRegistrations struct {
	*sqlite.Registrations
	hook    *admissionHook
	mu      sync.Mutex
	pending map[[32]byte]pendingRegister
}

func (s *admittingRegistrations) AddCheckedContext(c server.RegisterContext, reg *server.Registration) (*server.Registration, error) {
	// Admission identities must be based on transport-authenticated credentials.
	if !c.Identity.Secure() {
		return nil, errors.New("admission requires authenticated identity")
	}
	identity := c.Identity
	identity.Addr = ""
	identity.CertCN = ""
	var credential []byte
	switch identity.Mode {
	case server.ModePSK:
		credential = []byte(identity.PSKIdentity)
	case server.ModeRPK:
		credential = identity.PublicKey
	case server.ModeX509:
		if identity.Cert != nil {
			credential = identity.Cert.Raw
		}
	}
	if len(credential) == 0 {
		return nil, errors.New("admission: empty authenticated identity")
	}
	encoded, _ := json.Marshal(struct {
		Mode       server.SecurityMode
		Endpoint   string
		Credential []byte
	}{identity.Mode, c.Endpoint, credential})
	identityHash := sha256.Sum256(encoded)
	identityID := hex.EncodeToString(identityHash[:])
	fingerprint := hex.EncodeToString(c.Fingerprint[:])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admitted, err := s.hook.bindings.IsAdmitted(ctx, "default", identityID, fingerprint)
	if err != nil {
		return nil, server.ErrRegistrationUnavailable
	}
	var exchangeID string
	if c.Exchange != nil {
		s.mu.Lock()
		if s.pending == nil {
			s.pending = make(map[[32]byte]pendingRegister)
		}
		now := time.Now()
		for k, v := range s.pending {
			if now.Sub(v.at) >= 247*time.Second {
				delete(s.pending, k)
			}
		}
		entry := s.pending[*c.Exchange]
		if entry.id == "" && len(s.pending) >= 100000 {
			s.mu.Unlock()
			return nil, server.ErrRegistrationUnavailable
		}
		if entry.id == "" {
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				s.mu.Unlock()
				return nil, server.ErrRegistrationUnavailable
			}
			entry = pendingRegister{hex.EncodeToString(id[:]), now}
			s.pending[*c.Exchange] = entry
		}
		exchangeID = entry.id
		s.mu.Unlock()
	} else {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, server.ErrRegistrationUnavailable
		}
		exchangeID = hex.EncodeToString(id[:])
	}
	var old *server.Registration
	effect := func(tx *sql.Tx) error { var e error; old, e = s.Registrations.AddTx(ctx, tx, reg); return e }
	if admitted {
		err = s.hook.bindings.CommitExisting(ctx, exchangeID, "default", identityID, fingerprint, fingerprint, effect)
	} else {
		decision, e := s.hook.client.Decide(ctx, admission.Request{TenantID: "default", IdentityID: identityID, ExchangeID: exchangeID, Operation: "Register", RequestFingerprint: fingerprint, ObservedAt: time.Now().UTC()})
		if e != nil {
			return nil, server.ErrRegistrationUnavailable
		}
		if !decision.Admitted {
			return nil, errors.New("admission denied")
		}
		err = s.hook.bindings.CommitAdmitted(ctx, decision.Binding, decision.Receipt, effect)
	}
	if err != nil {
		return nil, server.ErrRegistrationUnavailable
	}
	if c.Exchange != nil {
		s.mu.Lock()
		delete(s.pending, *c.Exchange)
		s.mu.Unlock()
	}
	return old, nil
}
