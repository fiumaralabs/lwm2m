package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m"
)

// SecurityMode is /0/x/2 Security Mode (Core E.1).
type SecurityMode uint8

const (
	ModePSK   SecurityMode = 0
	ModeRPK   SecurityMode = 1
	ModeX509  SecurityMode = 2
	ModeNoSec SecurityMode = 3
	ModeEST   SecurityMode = 4
)

var modeNames = [...]string{"PSK", "RPK", "X509", "NO_SEC", "EST"}

// MarshalJSON writes the Leshan enum name ("PSK", "NO_SEC", ...).
func (m SecurityMode) MarshalJSON() ([]byte, error) {
	if int(m) >= len(modeNames) {
		return nil, fmt.Errorf("bootstrap: security mode %d", m)
	}
	return json.Marshal(modeNames[m])
}

// UnmarshalJSON reads the Leshan enum name or the resource value.
func (m *SecurityMode) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		for i, n := range modeNames {
			if n == s {
				*m = SecurityMode(i)
				return nil
			}
		}
		return fmt.Errorf("bootstrap: unknown security mode %q", s)
	}
	var n uint8
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*m = SecurityMode(n)
	return nil
}

// Bytes is an opaque value. Its JSON form is an array of byte values, as
// the Leshan bootstrap REST API sends it (zephyr-interop §4.3); signed
// Java bytes (-128..-1) are accepted too.
type Bytes []byte

func (b Bytes) MarshalJSON() ([]byte, error) {
	ints := make([]int, len(b))
	for i, c := range b {
		ints[i] = int(c)
	}
	return json.Marshal(ints)
}

func (b *Bytes) UnmarshalJSON(d []byte) error {
	var ints []int
	if err := json.Unmarshal(d, &ints); err != nil {
		return err
	}
	out := make(Bytes, len(ints))
	for i, v := range ints {
		if v < -128 || v > 255 {
			return fmt.Errorf("bootstrap: byte value %d out of range", v)
		}
		out[i] = byte(v)
	}
	*b = out
	return nil
}

// SecurityConfig is one /0 instance (Core E.1). Field names follow Leshan's
// BootstrapConfig.ServerSecurity so the bootstrap REST body maps 1:1.
type SecurityConfig struct {
	URI             string       `json:"uri"`                       // res 0
	BootstrapServer bool         `json:"bootstrapServer"`           // res 1
	SecurityMode    SecurityMode `json:"securityMode"`              // res 2
	PublicKeyOrID   Bytes        `json:"publicKeyOrId,omitempty"`   // res 3
	ServerPublicKey Bytes        `json:"serverPublicKey,omitempty"` // res 4
	SecretKey       Bytes        `json:"secretKey,omitempty"`       // res 5
	ServerID        *uint16      `json:"serverId,omitempty"`        // res 10
	// ClientOldOffTime is res 11, BootstrapServerAccountTimeout res 12
	// (BS-19: 0 = keep the account forever).
	ClientOldOffTime              *uint32 `json:"clientOldOffTime,omitempty"`
	BootstrapServerAccountTimeout *uint32 `json:"bootstrapServerAccountTimeout,omitempty"`
	SNI                           string  `json:"sni,omitempty"` // res 14
	// OSCORE is the /21 instance res 17 links to (Leshan "oscoreSecurityMode").
	OSCORE *uint16 `json:"oscoreSecurityMode,omitempty"`
	// ponytail: SMS resources 6-9, 13, 15, 16 are not modelled; use Writes.
}

// ServerConfig is one /1 instance (Core E.2), Leshan's ServerConfig.
type ServerConfig struct {
	ShortID                        uint16  `json:"shortId"`                                  // res 0
	Lifetime                       uint32  `json:"lifetime"`                                 // res 1
	DefaultMinPeriod               *uint32 `json:"defaultMinPeriod,omitempty"`               // res 2
	DefaultMaxPeriod               *uint32 `json:"defaultMaxPeriod,omitempty"`               // res 3
	DisableTimeout                 *uint32 `json:"disableTimeout,omitempty"`                 // res 5
	NotifIfDisabled                bool    `json:"notifIfDisabled"`                          // res 6
	Binding                        string  `json:"binding"`                                  // res 7, "U" when empty
	BootstrapOnRegistrationFailure *bool   `json:"bootstrapOnRegistrationFailure,omitempty"` // res 16
}

// ACLConfig is one /2 instance (Core E.3), Leshan's ACLConfig.
type ACLConfig struct {
	ObjectID         uint16            `json:"objectId"`           // res 0
	ObjectInstanceID uint16            `json:"objectInstanceId"`   // res 1; 65535 = Create rights (BS-25)
	ACLs             map[uint16]uint16 `json:"acls,omitempty"`     // res 2, keyed by ssid; 0 = default (BS-26)
	Owner            uint16            `json:"AccessControlOwner"` // res 3
}

// OSCOREConfig is one /21 instance (Core E.9), Leshan's OscoreObject.
type OSCOREConfig struct {
	MasterSecret  Bytes  `json:"oscoreMasterSecret"`            // res 0
	SenderID      Bytes  `json:"oscoreSenderId"`                // res 1
	RecipientID   Bytes  `json:"oscoreRecipientId"`             // res 2
	AEADAlgorithm *int64 `json:"oscoreAeadAlgorithm,omitempty"` // res 3
	HMACAlgorithm *int64 `json:"oscoreHmacAlgorithm,omitempty"` // res 4
	MasterSalt    Bytes  `json:"oscoreMasterSalt,omitempty"`    // res 5
	IDContext     Bytes  `json:"oscoreIdContext,omitempty"`     // res 6
}

// Write is a raw Bootstrap-Write: nodes under Path (an object, instance or
// resource), for objects the typed fields don't cover (/23, /24, vendor
// objects) or partial updates (BS-22).
type Write struct {
	Path  lwm2m.Path
	Nodes []lwm2m.Node
}

// BootstrapConfig is what the Bootstrap-Server provisions on one endpoint. Map keys
// are instance IDs. The JSON form is Leshan's BootstrapConfig (the
// Leshan-compatible REST layer decodes straight into it); fields tagged
// "-" are extensions.
type BootstrapConfig struct {
	// ContentFormat forces the Bootstrap-Write format; otherwise the
	// client's pct is used, else TLV (BS-02).
	ContentFormat *lwm2m.ContentFormat `json:"contentFormat,omitempty"`
	// AutoIDForSecurityObject runs a Bootstrap-Discover first and renumbers
	// /0 instances so they never collide with the client's BS account.
	AutoIDForSecurityObject bool                      `json:"autoIdForSecurityObject"`
	ToDelete                []string                  `json:"toDelete"`
	Servers                 map[uint16]ServerConfig   `json:"servers"`
	Security                map[uint16]SecurityConfig `json:"security"`
	ACLs                    map[uint16]ACLConfig      `json:"acls"`
	OSCORE                  map[uint16]OSCOREConfig   `json:"oscore"`

	Writes   []Write  `json:"-"` // sent after the typed instances
	Discover bool     `json:"-"` // Bootstrap-Discover "/" before the deletes (BS-05)
	Read     []string `json:"-"` // Bootstrap-Read after the writes: /1, /1/i, /2, /2/i only (BS-06)
	// RefusePack answers Bootstrap-Pack-Request with 4.05 so the client
	// falls back to Bootstrap-Request (BS-12).
	RefusePack bool `json:"-"`
}

var ErrInvalidConfig = errors.New("bootstrap: invalid config")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, a...)...)
}

// multiFormat reports a format a Bootstrap-Write or -Read may use (BS-03, BS-06).
func multiFormat(f lwm2m.ContentFormat) bool {
	switch f.Canonical() {
	case lwm2m.FormatTLV, lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR:
		return true
	}
	return false
}

func validSSID(id uint16) bool { return id != 0 && id != 65535 } // BS-17, Core Tbl 7.4-1

// Validate checks the config against the rules a Bootstrap-Server must
// follow when it provisions a client.
func (c *BootstrapConfig) Validate() error {
	if c.ContentFormat != nil && !multiFormat(*c.ContentFormat) {
		return invalid("content format %d cannot carry instances (BS-03)", *c.ContentFormat)
	}
	for _, d := range c.ToDelete {
		p, err := lwm2m.ParsePath(d)
		if err != nil || p.Len() > 2 {
			return invalid("toDelete %q: Bootstrap-Delete targets /, /o or /o/i (BS-04)", d)
		}
	}
	for _, r := range c.Read {
		p, err := lwm2m.ParsePath(r)
		if err != nil || p.Len() < 1 || p.Len() > 2 || (p.Object() != 1 && p.Object() != 2) {
			return invalid("read %q: Bootstrap-Read targets /1, /1/i, /2 or /2/i (BS-06)", r)
		}
	}
	bs := 0
	for id, s := range c.Security {
		if s.BootstrapServer {
			bs++
		} else if s.ServerID == nil || !validSSID(*s.ServerID) {
			return invalid("/0/%d: Short Server ID (res 10) must be 1..65534 (BS-17)", id)
		}
		if s.SecurityMode > ModeEST {
			return invalid("/0/%d: security mode %d", id, s.SecurityMode)
		}
		// BS-28: an RFC 7252 §6 style URI of at most 255 characters.
		u, err := url.Parse(s.URI)
		if len(s.URI) > 255 || err != nil || u.Scheme == "" || u.Host == "" && u.Opaque == "" {
			return invalid("/0/%d: server URI %q (BS-28)", id, s.URI)
		}
	}
	if bs > 1 {
		return invalid("more than one Bootstrap-Server account (BS-16)")
	}
	for id, s := range c.Servers {
		if !validSSID(s.ShortID) {
			return invalid("/1/%d: Short Server ID must be 1..65534 (BS-17)", id)
		}
	}
	for id, a := range c.ACLs {
		if a.ObjectInstanceID == 65535 && a.Owner != 65535 {
			return invalid("/2/%d: object-level Create rights need owner 65535 (BS-25)", id)
		}
		if a.Owner == 0 {
			return invalid("/2/%d: owner 0 is not a Short Server ID", id)
		}
		for ssid := range a.ACLs {
			if ssid == 65535 {
				return invalid("/2/%d: ACL for ssid 65535", id)
			}
		}
	}
	for _, w := range c.Writes {
		if w.Path.IsRoot() {
			return invalid("raw write on /")
		}
		for _, n := range w.Nodes {
			if !n.Path.HasPrefix(w.Path) {
				return invalid("raw write %s holds %s", w.Path, n.Path)
			}
		}
	}
	return nil
}

// hasServerAccount reports a complete LwM2M Server Account: a /0 instance
// with res 1 = false paired with a /1 instance by Short Server ID (BS-16).
func (c *BootstrapConfig) hasServerAccount() bool {
	for _, s := range c.Security {
		if s.BootstrapServer || s.ServerID == nil {
			continue
		}
		for _, srv := range c.Servers {
			if srv.ShortID == *s.ServerID {
				return true
			}
		}
	}
	return false
}

func sortedKeys[V any](m map[uint16]V) []uint16 {
	ks := make([]uint16, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

// securityIDs assigns /0 instance IDs. bsIDs are the client's BS account
// instances (from Bootstrap-Discover or acc): our BS account goes there,
// and no other instance may land on one of them.
func (c *BootstrapConfig) securityIDs(bsIDs []uint16) map[uint16]uint16 {
	out := map[uint16]uint16{}
	taken := map[uint16]bool{}
	for _, id := range bsIDs {
		taken[id] = true
	}
	var moved []uint16
	for _, id := range sortedKeys(c.Security) {
		switch {
		case c.Security[id].BootstrapServer && len(bsIDs) > 0:
			out[id] = bsIDs[0]
		case taken[id]:
			moved = append(moved, id)
		default:
			out[id] = id
			taken[id] = true
		}
	}
	next := uint16(0)
	for _, id := range moved {
		for taken[next] {
			next++
		}
		out[id] = next
		taken[next] = true
	}
	return out
}

func u32(p *uint32) int64 { return int64(*p) }

func (s SecurityConfig) nodes(p lwm2m.Path) []lwm2m.Node {
	r := func(id uint16, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(p.Append(id), v) }
	ns := []lwm2m.Node{r(0, lwm2m.String(s.URI)), r(1, lwm2m.Boolean(s.BootstrapServer)), r(2, lwm2m.Integer(int64(s.SecurityMode)))}
	// BS-24: NoSec leaves 3, 4, 5 null; PSK leaves 4 null; EST leaves 3 and
	// 5 null. We omit null resources (the RECOMMENDED form).
	m := s.SecurityMode
	if m != ModeNoSec && m != ModeEST {
		ns = append(ns, r(3, lwm2m.Opaque(s.PublicKeyOrID)))
	}
	if m != ModeNoSec && m != ModePSK {
		ns = append(ns, r(4, lwm2m.Opaque(s.ServerPublicKey)))
	}
	if m != ModeNoSec && m != ModeEST {
		ns = append(ns, r(5, lwm2m.Opaque(s.SecretKey)))
	}
	if s.ServerID != nil {
		ns = append(ns, r(10, lwm2m.Integer(int64(*s.ServerID))))
	}
	if s.ClientOldOffTime != nil {
		ns = append(ns, r(11, lwm2m.Integer(u32(s.ClientOldOffTime))))
	}
	if s.BootstrapServerAccountTimeout != nil {
		ns = append(ns, r(12, lwm2m.Integer(u32(s.BootstrapServerAccountTimeout))))
	}
	if s.SNI != "" {
		ns = append(ns, r(14, lwm2m.String(s.SNI)))
	}
	if s.OSCORE != nil {
		ns = append(ns, r(17, lwm2m.Objlnk(21, *s.OSCORE)))
	}
	return ns
}

func (s ServerConfig) nodes(p lwm2m.Path) []lwm2m.Node {
	r := func(id uint16, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(p.Append(id), v) }
	b := s.Binding
	if b == "" {
		b = "U"
	}
	ns := []lwm2m.Node{r(0, lwm2m.Integer(int64(s.ShortID))), r(1, lwm2m.Integer(int64(s.Lifetime)))}
	if s.DefaultMinPeriod != nil {
		ns = append(ns, r(2, lwm2m.Integer(u32(s.DefaultMinPeriod))))
	}
	if s.DefaultMaxPeriod != nil {
		ns = append(ns, r(3, lwm2m.Integer(u32(s.DefaultMaxPeriod))))
	}
	if s.DisableTimeout != nil {
		ns = append(ns, r(5, lwm2m.Integer(u32(s.DisableTimeout))))
	}
	ns = append(ns, r(6, lwm2m.Boolean(s.NotifIfDisabled)), r(7, lwm2m.String(b)))
	if s.BootstrapOnRegistrationFailure != nil {
		ns = append(ns, r(16, lwm2m.Boolean(*s.BootstrapOnRegistrationFailure)))
	}
	return ns
}

func (a ACLConfig) nodes(p lwm2m.Path) []lwm2m.Node {
	ns := []lwm2m.Node{
		lwm2m.ValueNode(p.Append(0), lwm2m.Integer(int64(a.ObjectID))),
		lwm2m.ValueNode(p.Append(1), lwm2m.Integer(int64(a.ObjectInstanceID))),
	}
	for _, ssid := range sortedKeys(a.ACLs) {
		ns = append(ns, lwm2m.ValueNode(p.Append(2).Append(ssid), lwm2m.Integer(int64(a.ACLs[ssid]))))
	}
	return append(ns, lwm2m.ValueNode(p.Append(3), lwm2m.Integer(int64(a.Owner))))
}

func (o OSCOREConfig) nodes(p lwm2m.Path) []lwm2m.Node {
	r := func(id uint16, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(p.Append(id), v) }
	ns := []lwm2m.Node{r(0, lwm2m.Opaque(o.MasterSecret)), r(1, lwm2m.Opaque(o.SenderID)), r(2, lwm2m.Opaque(o.RecipientID))}
	if o.AEADAlgorithm != nil {
		ns = append(ns, r(3, lwm2m.Integer(*o.AEADAlgorithm)))
	}
	if o.HMACAlgorithm != nil {
		ns = append(ns, r(4, lwm2m.Integer(*o.HMACAlgorithm)))
	}
	if len(o.MasterSalt) > 0 {
		ns = append(ns, r(5, lwm2m.Opaque(o.MasterSalt)))
	}
	if len(o.IDContext) > 0 {
		ns = append(ns, r(6, lwm2m.Opaque(o.IDContext)))
	}
	return ns
}

// plan returns the Bootstrap-Deletes and Bootstrap-Writes, in order: /0,
// /1, /2, /21 instances, then the raw writes. For a Pack (pack=true) the
// BS account instance is left out: the client keeps its own (BS-15).
func (c *BootstrapConfig) plan(bsIDs []uint16, pack bool) (deletes []lwm2m.Path, writes []Write) {
	for _, d := range c.ToDelete {
		deletes = append(deletes, lwm2m.MustParsePath(d))
	}
	ids := c.securityIDs(bsIDs)
	for _, id := range sortedKeys(c.Security) {
		s := c.Security[id]
		if pack && s.BootstrapServer {
			continue
		}
		p := lwm2m.NewPath(0, ids[id])
		writes = append(writes, Write{p, s.nodes(p)})
	}
	for _, id := range sortedKeys(c.Servers) {
		p := lwm2m.NewPath(1, id)
		writes = append(writes, Write{p, c.Servers[id].nodes(p)})
	}
	for _, id := range sortedKeys(c.ACLs) {
		p := lwm2m.NewPath(2, id)
		writes = append(writes, Write{p, c.ACLs[id].nodes(p)})
	}
	for _, id := range sortedKeys(c.OSCORE) {
		p := lwm2m.NewPath(21, id)
		writes = append(writes, Write{p, c.OSCORE[id].nodes(p)})
	}
	return deletes, append(writes, c.Writes...)
}

// packable reports why the config cannot be sent as a Bootstrap-Pack, or
// "" when it can. A Pack replaces every instance of each object it holds
// and nothing else (BS-15), so it needs a Server Account, and every delete
// and raw write must be within an object the Pack replaces as a whole.
func (c *BootstrapConfig) packable(writes []Write) string {
	if c.RefusePack {
		return "refused by config"
	}
	if !c.hasServerAccount() {
		return "no LwM2M Server Account (BS-15)"
	}
	objs := map[uint16]bool{}
	for _, w := range writes {
		objs[w.Path.Object()] = true
	}
	for _, w := range c.Writes {
		if w.Path.Len() > 2 {
			return "resource-level write " + w.Path.String()
		}
	}
	for _, d := range c.ToDelete {
		p := lwm2m.MustParsePath(d)
		if p.IsRoot() || !objs[p.Object()] {
			return "delete " + d + " outside the Pack"
		}
	}
	return ""
}

// ConfigStore resolves the bootstrap config of an endpoint. Implementations
// must be safe for concurrent use.
type ConfigStore interface {
	Get(ep string) (*BootstrapConfig, bool)
}

// MemoryConfigStore is an in-memory ConfigStore.
type MemoryConfigStore struct {
	mu sync.RWMutex
	m  map[string]*BootstrapConfig
}

func NewMemoryConfigStore() *MemoryConfigStore {
	return &MemoryConfigStore{m: map[string]*BootstrapConfig{}}
}

// Put validates and stores the config of ep.
func (s *MemoryConfigStore) Put(ep string, c *BootstrapConfig) error {
	if strings.TrimSpace(ep) == "" {
		return invalid("empty endpoint")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	s.m[ep] = c
	s.mu.Unlock()
	return nil
}

func (s *MemoryConfigStore) Get(ep string) (*BootstrapConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[ep]
	return c, ok
}

// Delete removes the config of ep and reports whether it existed.
func (s *MemoryConfigStore) Delete(ep string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[ep]
	delete(s.m, ep)
	return ok
}
