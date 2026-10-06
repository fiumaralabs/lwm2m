package leshanapi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// This file holds the Leshan demo JSON shapes (zephyr-interop.md §3.4-§3.7):
// JacksonResponseSerializer, JacksonLwM2mNodeSerializer/Deserializer,
// JacksonRegistrationSerializer and JacksonLinkSerializer.

// leshanType names a value type as Leshan's ResourceModel.Type enum.
var leshanType = map[lwm2m.Type]string{
	lwm2m.TypeNone: "NONE", lwm2m.TypeString: "STRING", lwm2m.TypeInteger: "INTEGER",
	lwm2m.TypeUnsigned: "UNSIGNED_INTEGER", lwm2m.TypeFloat: "FLOAT", lwm2m.TypeBoolean: "BOOLEAN",
	lwm2m.TypeOpaque: "OPAQUE", lwm2m.TypeTime: "TIME", lwm2m.TypeObjlnk: "OBJLNK", lwm2m.TypeCorelnk: "CORELINK",
}

// formatByName is ContentFormat.fromName (§3.2); unknown names are nil.
var formatByName = map[string]lwm2m.ContentFormat{
	"TLV": lwm2m.FormatTLV, "JSON": lwm2m.FormatOMAJSON, "TEXT": lwm2m.FormatText,
	"OPAQUE": lwm2m.FormatOpaque, "LINK": lwm2m.FormatLinkFormat, "SENML_JSON": lwm2m.FormatSenMLJSON,
	"SENML_CBOR": lwm2m.FormatSenMLCBOR, "CBOR": lwm2m.FormatCBOR,
}

func formatParam(v string) *lwm2m.ContentFormat {
	f, ok := formatByName[strings.ToUpper(v)]
	if !ok {
		return nil
	}
	return &f
}

// codeNames are Leshan's known ResponseCodes; others print as UNKNOWN.
var codeNames = map[int]string{
	201: "CREATED", 202: "DELETED", 204: "CHANGED", 205: "CONTENT", 400: "BAD_REQUEST",
	401: "UNAUTHORIZED", 403: "FORBIDDEN", 404: "NOT_FOUND", 405: "METHOD_NOT_ALLOWED",
	406: "NOT_ACCEPTABLE", 408: "REQUEST_ENTITY_INCOMPLETE", 412: "PRECONDITION_FAILED",
	413: "REQUEST_ENTITY_TOO_LARGE", 415: "UNSUPPORTED_CONTENT_FORMAT", 500: "INTERNAL_SERVER_ERROR",
}

// statusString renders a CoAP code as Leshan's "NAME(code)", e.g. 2.05 is
// "CONTENT(205)".
func statusString(c codes.Code) string {
	n := int(c>>5)*100 + int(c&0x1f)
	name, ok := codeNames[n]
	if !ok {
		name = "UNKNOWN"
	}
	return fmt.Sprintf("%s(%d)", name, n)
}

// responseJSON is JacksonResponseSerializer: a LinkedHashMap in this order.
type responseJSON struct {
	Status       string     `json:"status"`
	Valid        bool       `json:"valid"`
	Success      bool       `json:"success"`
	Failure      bool       `json:"failure"`
	Content      any        `json:"content,omitempty"`
	ObjectLinks  []linkJSON `json:"objectLinks,omitempty"`
	Location     string     `json:"location,omitempty"`
	ErrorMessage string     `json:"errormessage,omitempty"`
}

func newResponse(c codes.Code, diag []byte) responseJSON {
	ok := c>>5 == 2
	r := responseJSON{Status: statusString(c), Valid: true, Success: ok, Failure: !ok}
	if !ok {
		r.ErrorMessage = string(diag) // CoAP diagnostic payload
	}
	return r
}

type linkJSON struct {
	URL        string            `json:"url"`
	Attributes map[string]string `json:"attributes"`
}

// linksJSON is JacksonLinkSerializer over a link-format payload. Attribute
// values are their core-link form (quoted strings keep their quotes).
func linksJSON(payload []byte) ([]linkJSON, error) {
	ls, err := link.Parse(string(payload))
	if err != nil {
		return nil, err
	}
	out := make([]linkJSON, 0, len(ls))
	for _, l := range ls {
		a := map[string]string{}
		for _, p := range l.Params {
			v := p.Value
			if p.Quoted {
				v = strconv.Quote(v)
			}
			a[p.Name] = v
		}
		out = append(out, linkJSON{URL: l.URI, Attributes: a})
	}
	return out, nil
}

// --- LwM2mNode -------------------------------------------------------------

// javaDouble formats f like Java's Double.toString, which Leshan uses for
// FLOAT values.
func javaDouble(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	if a := math.Abs(f); a >= 1e-3 && a < 1e7 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	m, e, _ := strings.Cut(strconv.FormatFloat(f, 'E', -1, 64), "E")
	if !strings.Contains(m, ".") {
		m += ".0"
	}
	n, _ := strconv.Atoi(e)
	return m + "E" + strconv.Itoa(n)
}

// jsonValue is JacksonLwM2mNodeSerializer.convertValue (§3.7).
func jsonValue(v lwm2m.Value) any {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str
	case lwm2m.TypeInteger:
		return strconv.FormatInt(v.Int, 10)
	case lwm2m.TypeUnsigned:
		return strconv.FormatUint(v.Uint, 10)
	case lwm2m.TypeFloat:
		return javaDouble(v.Float)
	case lwm2m.TypeBoolean:
		return v.Bool
	case lwm2m.TypeOpaque:
		return hex.EncodeToString(v.Bytes)
	case lwm2m.TypeTime:
		return v.Int * 1000 // java.util.Date: epoch milliseconds
	case lwm2m.TypeObjlnk:
		return map[string]any{"objectId": v.Link.Object, "objectInstanceId": v.Link.Instance, "nullLink": v.Link == lwm2m.NullObjLink}
	}
	return nil
}

// encodeNode builds the Leshan node at p from flat nodes. ok=false means
// the payload holds nothing at p; callers omit such paths (§3.4).
func encodeNode(p lwm2m.Path, all []lwm2m.Node, sch lwm2m.Schema) (any, bool) {
	var ns []lwm2m.Node
	for _, n := range all {
		if n.Path.HasPrefix(p) {
			ns = append(ns, n)
		}
	}
	if p.IsRoot() {
		return map[string]any{"kind": "root", "objects": children(p, ns, sch)}, true
	}
	if len(ns) == 0 {
		return nil, false
	}
	id := p.ID(p.Len() - 1)
	switch p.Len() {
	case 1:
		return map[string]any{"id": id, "kind": "obj", "instances": children(p, ns, sch)}, true
	case 2:
		return map[string]any{"id": id, "kind": "instance", "resources": children(p, ns, sch)}, true
	case 4:
		return map[string]any{"id": id, "kind": "resourceInstance", "type": leshanType[ns[0].Value.Type], "value": jsonValue(ns[0].Value)}, true
	}
	var def lwm2m.ResourceDef
	if sch != nil {
		def, _ = sch.Resource(p)
	}
	multi, typ := def.Multiple, def.Type
	for _, n := range ns {
		if n.Path.IsResourceInstance() || n.Kind == lwm2m.KindEmptyMultiple {
			multi = true
		}
		if n.Kind == lwm2m.KindValue {
			typ = n.Value.Type
		}
		if n.Path == p && n.Kind == lwm2m.KindValue {
			return map[string]any{"id": id, "kind": "singleResource", "type": leshanType[typ], "value": jsonValue(n.Value)}, true
		}
	}
	if !multi {
		return nil, false
	}
	values := map[string]any{}
	for _, n := range ns {
		if n.Path.IsResourceInstance() && n.Kind == lwm2m.KindValue {
			values[strconv.Itoa(int(n.Path.ResourceInstance()))] = jsonValue(n.Value)
		}
	}
	return map[string]any{"id": id, "kind": "multiResource", "type": leshanType[typ], "values": values}, true
}

// children encodes the distinct child levels of p present in ns, by id.
func children(p lwm2m.Path, ns []lwm2m.Node, sch lwm2m.Schema) []any {
	seen := map[uint16]bool{}
	var ids []int
	for _, n := range ns {
		if n.Path.Len() > p.Len() {
			if id := n.Path.ID(p.Len()); !seen[id] {
				seen[id] = true
				ids = append(ids, int(id))
			}
		}
	}
	sort.Ints(ids)
	out := []any{}
	for _, id := range ids {
		if v, ok := encodeNode(p.Append(uint16(id)), ns, sch); ok {
			out = append(out, v)
		}
	}
	return out
}

// compositeContent maps each requested path to its node, omitting paths
// absent from the payload (never null, §3.4).
func compositeContent(paths []lwm2m.Path, nodes []lwm2m.Node, sch lwm2m.Schema) map[string]any {
	out := map[string]any{}
	for _, p := range paths {
		if v, ok := encodeNode(p, nodes, sch); ok {
			out[p.String()] = v
		}
	}
	return out
}

// jsonNode is the input form JacksonLwM2mNodeDeserializer reads.
type jsonNode struct {
	Kind      string                     `json:"kind"`
	ID        json.RawMessage            `json:"id"`
	Instances []jsonNode                 `json:"instances"`
	Resources []jsonNode                 `json:"resources"`
	Values    map[string]json.RawMessage `json:"values"`
	Value     json.RawMessage            `json:"value"`
	Type      string                     `json:"type"`
}

// id reads "id" with Jackson's asInt: 1 and "1" both work.
func (j jsonNode) id() (uint16, bool) {
	s := strings.Trim(string(j.ID), `"`)
	n, err := strconv.ParseUint(s, 10, 16)
	return uint16(n), err == nil && n < lwm2m.MaxID
}

// nodes flattens j, which sits at p, following the deserializer's kind
// detection order (ND:76-138).
func (j jsonNode) nodes(p lwm2m.Path) ([]lwm2m.Node, error) {
	child := func(c jsonNode) ([]lwm2m.Node, error) {
		id, ok := c.id()
		if !ok {
			return nil, fmt.Errorf("invalid id %s", c.ID)
		}
		return c.nodes(p.Append(id))
	}
	var out []lwm2m.Node
	switch {
	case j.Kind == "obj" || j.Instances != nil:
		for _, c := range j.Instances {
			ns, err := child(c)
			if err != nil {
				return nil, err
			}
			out = append(out, ns...)
		}
	case j.Kind == "instance" || j.Resources != nil:
		if p.Len() != 2 {
			return nil, fmt.Errorf("instance node at %s", p)
		}
		if len(j.Resources) == 0 {
			return []lwm2m.Node{{Path: p, Kind: lwm2m.KindEmptyInstance}}, nil
		}
		for _, c := range j.Resources {
			ns, err := child(c)
			if err != nil {
				return nil, err
			}
			out = append(out, ns...)
		}
	case j.Kind == "multiResource" || j.Values != nil:
		if p.Len() != 3 {
			return nil, fmt.Errorf("multiResource node at %s", p)
		}
		if len(j.Values) == 0 {
			return []lwm2m.Node{{Path: p, Kind: lwm2m.KindEmptyMultiple}}, nil
		}
		for k, raw := range j.Values {
			ri, err := strconv.ParseUint(k, 10, 16)
			if err != nil || ri >= lwm2m.MaxID {
				return nil, fmt.Errorf("invalid resource instance id %q", k)
			}
			v, err := parseValue(j.Type, raw)
			if err != nil {
				return nil, err
			}
			out = append(out, lwm2m.ValueNode(p.Append(uint16(ri)), v))
		}
		lwm2m.SortNodes(out)
	case j.Value != nil:
		if want := 3 + b2i(j.Kind == "resourceInstance"); p.Len() != want {
			return nil, fmt.Errorf("%s node at %s", j.Kind, p)
		}
		v, err := parseValue(j.Type, j.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, lwm2m.ValueNode(p, v))
	default:
		return nil, fmt.Errorf("invalid node")
	}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseValue follows ND:146-262 for each type name (case-insensitive).
func parseValue(typ string, raw json.RawMessage) (lwm2m.Value, error) {
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		return lwm2m.Value{}, err
	}
	num := func() (string, bool) {
		switch t := x.(type) {
		case string:
			return t, true
		case float64:
			return string(raw), true
		}
		return "", false
	}
	bad := fmt.Errorf("invalid %s value %s", typ, raw)
	switch strings.ToUpper(typ) {
	case "STRING":
		if s, ok := x.(string); ok {
			return lwm2m.String(s), nil
		}
	case "CORELINK":
		if s, ok := x.(string); ok {
			return lwm2m.Corelnk(s), nil
		}
	case "BOOLEAN":
		if b, ok := x.(bool); ok {
			return lwm2m.Boolean(b), nil
		}
	case "INTEGER":
		if s, ok := num(); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return lwm2m.Integer(n), nil
			}
		}
	case "UNSIGNED_INTEGER":
		if s, ok := num(); ok {
			if n, err := strconv.ParseUint(s, 10, 64); err == nil {
				return lwm2m.Unsigned(n), nil
			}
		}
	case "FLOAT":
		if s, ok := num(); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return lwm2m.Float(f), nil
			}
		}
	case "TIME":
		// Leshan reads new Date(n): milliseconds (the harness sends seconds,
		// zephyr-interop §5 quirk; only int-257 does, with 0).
		if _, ok := x.(float64); ok {
			if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
				return lwm2m.Time(n / 1000), nil
			}
		}
	case "OPAQUE":
		if s, ok := x.(string); ok {
			if b, err := hex.DecodeString(s); err == nil {
				return lwm2m.Opaque(b), nil
			}
		}
	case "OBJLNK":
		var l struct{ ObjectID, ObjectInstanceID *uint16 }
		if json.Unmarshal(raw, &l) == nil && l.ObjectID != nil && l.ObjectInstanceID != nil {
			return lwm2m.Objlnk(*l.ObjectID, *l.ObjectInstanceID), nil
		}
	}
	return lwm2m.Value{}, bad
}

// --- Registration ----------------------------------------------------------

// registrationJSON is JacksonRegistrationSerializer (§3.5).
type registrationJSON struct {
	Endpoint                         string              `json:"endpoint"`
	RegistrationID                   string              `json:"registrationId"`
	RegistrationDate                 int64               `json:"registrationDate"`
	LastUpdate                       int64               `json:"lastUpdate"`
	Address                          string              `json:"address"`
	SMSNumber                        string              `json:"smsNumber,omitempty"`
	LwM2mVersion                     string              `json:"lwM2mVersion"`
	Lifetime                         int64               `json:"lifetime"`
	BindingMode                      string              `json:"bindingMode"`
	RootPath                         string              `json:"rootPath"`
	ObjectLinks                      []linkJSON          `json:"objectLinks"`
	Secure                           bool                `json:"secure"`
	AdditionalRegistrationAttributes map[string]string   `json:"additionalRegistrationAttributes"`
	QueueMode                        bool                `json:"queuemode"`
	AvailableInstances               map[string][]uint16 `json:"availableInstances"`
	Sleeping                         *bool               `json:"sleeping,omitempty"`
}

// leshanAddr prints host:port without IPv6 brackets, like Leshan.
func leshanAddr(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, p, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h + ":" + p
}

func newRegistrationJSON(r *server.Registration, sleeping *bool) registrationJSON {
	j := registrationJSON{
		Endpoint: r.Endpoint, RegistrationID: r.ID,
		RegistrationDate: r.RegisteredAt.UnixMilli(), LastUpdate: r.LastUpdate.UnixMilli(),
		Address: leshanAddr(r.Addr), SMSNumber: r.SMS, LwM2mVersion: r.Version,
		Lifetime: int64(r.Lifetime.Seconds()), BindingMode: r.Binding, RootPath: r.RootPath,
		ObjectLinks: []linkJSON{}, Secure: r.Identity.Secure(),
		AdditionalRegistrationAttributes: map[string]string{},
		QueueMode:                        r.QueueMode, AvailableInstances: map[string][]uint16{},
	}
	if j.BindingMode == "" {
		j.BindingMode = "U"
	}
	if j.RootPath == "" {
		j.RootPath = "/"
	}
	// ponytail: objectLinks are rebuilt from the parsed object list (the
	// native Registration keeps no raw links), so </> and unknown link
	// attributes are not echoed. The harness reads none of them.
	for _, o := range r.Objects {
		a := map[string]string{}
		if o.Version != "" {
			a["ver"] = o.Version
		}
		if len(o.Instances) == 0 {
			j.ObjectLinks = append(j.ObjectLinks, linkJSON{URL: lwm2m.NewPath(o.ID).String(), Attributes: a})
			continue
		}
		insts := append([]uint16(nil), o.Instances...)
		sort.Slice(insts, func(a, b int) bool { return insts[a] < insts[b] })
		j.AvailableInstances[strconv.Itoa(int(o.ID))] = insts
		for i, iid := range insts {
			la := map[string]string{}
			if i == 0 {
				la = a
			}
			j.ObjectLinks = append(j.ObjectLinks, linkJSON{URL: lwm2m.NewPath(o.ID, iid).String(), Attributes: la})
		}
	}
	if r.QueueMode {
		j.Sleeping = sleeping
		if j.Sleeping == nil {
			f := false
			j.Sleeping = &f
		}
	}
	return j
}

// --- SecurityInfo ----------------------------------------------------------

// securityJSON is JacksonSecuritySerializer/Deserializer (§3.6).
type securityJSON struct {
	Endpoint string          `json:"endpoint"`
	TLS      *tlsJSON        `json:"tls,omitempty"`
	OSCORE   json.RawMessage `json:"oscore,omitempty"`
}

type tlsJSON struct {
	Mode    string       `json:"mode"`
	Details *detailsJSON `json:"details,omitempty"`
}

type detailsJSON struct {
	Identity string `json:"identity,omitempty"`
	Key      string `json:"key"`
}

func newSecurityJSON(si server.SecurityInfo) securityJSON {
	j := securityJSON{Endpoint: si.Endpoint}
	switch {
	case si.PSKIdentity != "":
		j.TLS = &tlsJSON{"psk", &detailsJSON{si.PSKIdentity, hex.EncodeToString(si.PSKKey)}}
	case len(si.PublicKey) > 0:
		j.TLS = &tlsJSON{"rpk", &detailsJSON{Key: hex.EncodeToString(si.PublicKey)}}
	case si.X509:
		j.TLS = &tlsJSON{Mode: "x509"}
	}
	return j
}

func (j securityJSON) info() (server.SecurityInfo, error) {
	si := server.SecurityInfo{Endpoint: j.Endpoint}
	if j.Endpoint == "" {
		return si, fmt.Errorf("Missing endpoint")
	}
	if j.TLS == nil {
		// ponytail: OSCORE security info lives in the oscore package, not
		// server.SecurityInfo; reject until the native store carries it.
		return si, fmt.Errorf("Invalid security info content")
	}
	var key []byte
	if j.TLS.Details != nil {
		var err error
		if key, err = hex.DecodeString(j.TLS.Details.Key); err != nil {
			return si, fmt.Errorf("key parameter must be a valid hex string")
		}
	}
	switch j.TLS.Mode {
	case "psk":
		if j.TLS.Details == nil || j.TLS.Details.Identity == "" {
			return si, fmt.Errorf("Missing PSK identity")
		}
		si.PSKIdentity, si.PSKKey = j.TLS.Details.Identity, key
	case "rpk":
		if len(key) == 0 {
			return si, fmt.Errorf("Invalid security info content")
		}
		si.PublicKey = key
	case "x509":
		si.X509 = true
	default:
		return si, fmt.Errorf("Invalid security info content")
	}
	return si, nil
}
