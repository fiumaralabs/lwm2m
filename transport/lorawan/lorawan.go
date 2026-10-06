// Package lorawan is the LoRaWAN binding of the LwM2M Server: the Server
// runs as a LoRaWAN Application Server (T §6.8.4, App. C), with CoAP in
// the FRMPayload of one FPort, through a pluggable Network Server.
package lorawan

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/internal/regparam"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// App. C.2 transmission parameters and the Tbl 6.8.4-1 lifetime default.
const (
	AckTimeout      = 300 * time.Second
	MaxRetransmit   = 4
	DefaultLifetime = 2592000 // 30 days
)

// NetworkServer queues a downlink for a LoRaWAN endpoint. Downlink
// returns when the Network Server notifies that the frame was sent to the
// endpoint, or with an error when sending failed (App. C.3.1.2, C.3.2.2).
// Uplinks are handed to Binding.Uplink.
type NetworkServer interface {
	Downlink(ctx context.Context, devEUI string, fport uint8, payload []byte, confirmed bool) error
}

const codeConflict codes.Code = 137 // 4.09, unnamed in go-coap

// Config configures a Binding.
type Config struct {
	Server *server.Server
	NS     NetworkServer
	// FPort carries LwM2M, from the Server URI lorawan://{port}.
	FPort uint8
	// Confirmed sends LoRaWAN confirmed downlinks: the Network Server
	// retransmits, so CoAP does not (App. C.3.1.2).
	Confirmed bool
	// Objects, if set, returns the object list (CoRE link format) of a
	// device known out of band, "" when unknown. It fills a Register sent
	// without one (T §6.8.4).
	Objects func(devEUI string) string
	// AckTimeout and MaxRetransmit default to App. C.2 (300 s, 4).
	AckTimeout    time.Duration
	MaxRetransmit int
}

// Binding is the LoRaWAN Application Server of one server.
type Binding struct {
	cfg   Config
	mu    sync.Mutex
	conns map[string]*coapwire.Conn
}

// ParseServerURI returns the FPort of "lorawan://{port}" (1-255).
func ParseServerURI(uri string) (uint8, error) {
	p, ok := strings.CutPrefix(uri, "lorawan://")
	n, err := strconv.ParseUint(p, 10, 8)
	if !ok || err != nil || n == 0 || strings.Trim(p, "0123456789") != "" {
		return 0, fmt.Errorf("lorawan: %q is not lorawan://{1-255}", uri)
	}
	return uint8(n), nil
}

func New(cfg Config) (*Binding, error) {
	if cfg.FPort == 0 {
		return nil, errors.New("lorawan: FPort must be 1-255")
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = AckTimeout
	}
	if cfg.MaxRetransmit == 0 && !cfg.Confirmed {
		cfg.MaxRetransmit = MaxRetransmit
	}
	if cfg.Confirmed {
		cfg.MaxRetransmit = 0
	}
	return &Binding{cfg: cfg, conns: map[string]*coapwire.Conn{}}, nil
}

// Peer returns the server.Peer of a device. Security comes from LoRaWAN,
// so the session is NoSec, identified by DevEUI.
func (b *Binding) Peer(devEUI string) *coapwire.Conn {
	devEUI = strings.ToUpper(devEUI)
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.conns[devEUI]; ok {
		return c
	}
	addr := coapwire.Addr{Net: "lorawan", ID: devEUI}
	c := coapwire.NewConn(coapwire.Config{
		Server:        b.cfg.Server,
		Identity:      server.Identity{Mode: server.ModeNoSec, Addr: addr.String()},
		Addr:          addr,
		AckTimeout:    b.cfg.AckTimeout,
		MaxRetransmit: b.cfg.MaxRetransmit,
		Send: func(ctx context.Context, p []byte) error {
			return b.cfg.NS.Downlink(ctx, devEUI, b.cfg.FPort, p, b.cfg.Confirmed)
		},
		Intercept: func(m *server.Message) *server.Message { return b.register(devEUI, m) },
	})
	b.conns[devEUI] = c
	return c
}

// Uplink handles one uplink frame. Frames on other FPorts are not LwM2M.
func (b *Binding) Uplink(ctx context.Context, devEUI string, fport uint8, payload []byte) error {
	if fport != b.cfg.FPort {
		return nil
	}
	return b.Peer(devEUI).Receive(ctx, payload)
}

// register applies the Tbl 6.8.4-1 defaults to a Register: ep is the
// DevEUI, lt 30 days, lwm2m 1.1, and queue mode. Without an object list
// it uses the out-of-band one, or answers 4.09.
func (b *Binding) register(devEUI string, m *server.Message) *server.Message {
	if m.Code != codes.POST || strings.Trim(m.Path, "/") != "rd" {
		return nil
	}
	q, err := regparam.SplitQuery(m.Query)
	if err != nil {
		return nil // the core answers 4.00
	}
	for k, v := range map[string]string{"ep": devEUI, "lt": strconv.Itoa(DefaultLifetime), "lwm2m": "1.1"} {
		if _, ok := q[k]; !ok {
			m.Query = append(m.Query, k+"="+v)
		}
	}
	if _, ok := q["Q"]; !ok {
		m.Query = append(m.Query, "Q")
	}
	if len(m.Payload) == 0 {
		objs := ""
		if b.cfg.Objects != nil {
			objs = b.cfg.Objects(devEUI)
		}
		if objs == "" {
			return &server.Message{Code: codeConflict}
		}
		f := lwm2m.FormatLinkFormat
		m.Payload, m.Format = []byte(objs), &f
	}
	return nil
}
