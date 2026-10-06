// Package niddbinding is LwM2M binding N: CoAP over 3GPP Non-IP Data
// Delivery (T §6.8.5, App. D) through a pluggable SCEF or NEF.
package niddbinding

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/server"
)

// MTU is the Non-IP link MTU (3GPP 24.301 §6.6.4.2, T App. D.4). NAS has
// no segmentation, so no downlink message may exceed it.
const MTU = 1358

// SCEF submits mobile-terminated Non-IP data to a UE (the T8 NIDD API of
// 3GPP 29.122, or the NEF's of 29.522). The UE is named by its External
// Identifier or MSISDN (T App. D.3). Mobile-originated data is handed to
// Binding.Deliver.
type SCEF interface {
	Submit(ctx context.Context, ue string, data []byte) error
}

// Config configures a Binding.
type Config struct {
	Server *server.Server
	SCEF   SCEF
	// MTU is the downlink limit; 0 is the 1358-byte Non-IP MTU. A lower
	// value comes from the PCO maximum packet size.
	MTU int
	// AckTimeout and MaxRetransmit are the RFC 7252 defaults (2 s, 4)
	// when zero. Keep them small when the SCEF buffers downlink for
	// sleeping UEs, so queue mode and network buffering do not stack
	// (T App. D).
	AckTimeout    time.Duration
	MaxRetransmit int
}

// Binding is the NIDD binding of one server.
type Binding struct {
	cfg   Config
	mu    sync.Mutex
	conns map[string]*coapwire.Conn
}

func New(cfg Config) *Binding {
	if cfg.MTU == 0 {
		cfg.MTU = MTU
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = 2 * time.Second
	}
	if cfg.MaxRetransmit == 0 {
		cfg.MaxRetransmit = 4
	}
	return &Binding{cfg: cfg, conns: map[string]*coapwire.Conn{}}
}

// ValidUE accepts an External Identifier ("local@domain", 3GPP 23.003
// §19.7.2) or an MSISDN (digits, optional "+").
func ValidUE(ue string) error {
	if l, d, ok := strings.Cut(ue, "@"); ok && l != "" && d != "" && !strings.Contains(d, "@") {
		return nil
	}
	if d := strings.TrimPrefix(ue, "+"); d != "" && len(d) <= 15 && strings.Trim(d, "0123456789") == "" {
		return nil
	}
	return fmt.Errorf("niddbinding: %q is neither an External Identifier nor an MSISDN", ue)
}

// Peer returns the server.Peer of ue.
func (b *Binding) Peer(ue string) (*coapwire.Conn, error) {
	if err := ValidUE(ue); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.conns[ue]; ok {
		return c, nil
	}
	addr := coapwire.Addr{Net: "nidd", ID: ue}
	c := coapwire.NewConn(coapwire.Config{
		Server:        b.cfg.Server,
		Binding:       "N",
		Identity:      server.Identity{Mode: server.ModeNoSec, Addr: addr.String()},
		Addr:          addr,
		AckTimeout:    b.cfg.AckTimeout,
		MaxRetransmit: b.cfg.MaxRetransmit,
		Backoff:       true,
		MaxSize:       b.cfg.MTU,
		Send:          func(ctx context.Context, p []byte) error { return b.cfg.SCEF.Submit(ctx, ue, p) },
	})
	b.conns[ue] = c
	return c, nil
}

// Deliver handles mobile-originated Non-IP data from ue.
func (b *Binding) Deliver(ctx context.Context, ue string, data []byte) error {
	c, err := b.Peer(ue)
	if err != nil {
		return err
	}
	return c.Receive(ctx, data)
}
