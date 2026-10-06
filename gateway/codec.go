package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/codec/lwm2mcbor"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/fxamacker/cbor/v2"
)

// In LwM2M CBOR, SenML JSON/CBOR and SenML-ETCH the prefix is part of the
// names (GW §9). Formats without names (TLV, text, opaque, CBOR) carry
// device data only under a prefixed request path, which then applies to
// every node.

func senmlCodec(cf lwm2m.ContentFormat) (senml.Codec, bool) {
	switch cf.Canonical() {
	case lwm2m.FormatSenMLJSON:
		return senml.JSON, true
	case lwm2m.FormatSenMLCBOR:
		return senml.CBOR, true
	case lwm2m.FormatSenMLETCHJSON:
		return senml.ETCHJSON, true
	case lwm2m.FormatSenMLETCHCBOR:
		return senml.ETCHCBOR, true
	}
	return senml.Codec{}, false
}

// Decode decodes a payload whose request path was base. Names may carry
// prefixes; a name without one inside a prefixed request belongs to that
// device (tolerated: the prefix is mandatory in names, GW §9).
func Decode(cf lwm2m.ContentFormat, base Path, data []byte, s lwm2m.Schema) ([]Node, error) {
	var out []Node
	switch c, isSenML := senmlCodec(cf); {
	case cf.Canonical() == lwm2m.FormatLwM2MCBOR:
		ns, err := lwm2mcbor.DecodePrefixed(base.Path, data, s)
		if err != nil {
			return nil, err
		}
		out = ns
	case isSenML:
		ns, err := decodeSenML(c, base, data, s)
		if err != nil {
			return nil, err
		}
		out = ns
	default:
		c, err := codec.For(cf)
		if err != nil {
			return nil, err
		}
		ns, err := c.Decode(base.Path, data, s)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			out = append(out, Node{Prefix: base.Prefix, Node: n})
		}
	}
	for i := range out {
		if out[i].Prefix == "" {
			out[i].Prefix = base.Prefix
		} else if base.Prefix != "" && out[i].Prefix != base.Prefix {
			return nil, fmt.Errorf("gateway: node of %q in a response for %q", out[i].Prefix, base.Prefix)
		}
	}
	return out, nil
}

// splitName separates a prefix from a resolved SenML name.
func splitName(name string) (prefix, rest string) {
	s := strings.TrimPrefix(name, "/")
	head, tail, _ := strings.Cut(s, "/")
	if head == "" || strings.Trim(head, "0123456789") == "" {
		return "", name
	}
	return head, "/" + tail
}

// decodeSenML resolves base names and times (RFC 8428 §4.6), splits the
// records by prefix and decodes each group with the plain codec.
func decodeSenML(c senml.Codec, base Path, data []byte, s lwm2m.Schema) ([]Node, error) {
	rs, err := c.DecodeRecords(data)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		ns, err := c.Decode(base.Path, data, s)
		out := make([]Node, len(ns))
		for i, n := range ns {
			out[i] = Node{Prefix: base.Prefix, Node: n}
		}
		return out, err
	}
	var order []string
	groups := map[string][]senml.Record{}
	bn, bt := "", 0.0
	for _, r := range rs {
		if r.BaseName != "" {
			bn = r.BaseName
		}
		if r.BaseTime != 0 {
			bt = r.BaseTime
		}
		t := bt + r.Time
		if r.Time != 0 && bt != 0 {
			t = math.Round(t*1e6) / 1e6
		}
		pre, name := splitName(bn + r.Name)
		if _, ok := groups[pre]; !ok {
			order = append(order, pre)
		}
		r.BaseName, r.Name, r.BaseTime, r.Time = "", name, 0, t
		groups[pre] = append(groups[pre], r)
	}
	var out []Node
	for _, pre := range order {
		enc, err := c.EncodeRecords(groups[pre])
		if err != nil {
			return nil, err
		}
		gb := lwm2m.Root
		if pre == base.Prefix || pre == "" {
			gb = base.Path
		}
		ns, err := c.Decode(gb, enc, s)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			out = append(out, Node{Prefix: pre, Node: n})
		}
	}
	return out, nil
}

// group splits nodes by prefix, keeping first-appearance order.
func group(nodes []Node) ([]string, map[string][]lwm2m.Node) {
	var order []string
	g := map[string][]lwm2m.Node{}
	for _, n := range nodes {
		if _, ok := g[n.Prefix]; !ok {
			order = append(order, n.Prefix)
		}
		g[n.Prefix] = append(g[n.Prefix], n.Node)
	}
	return order, g
}

// Encode encodes nodes for a request on base (a Write, Create or
// Write-Composite body; root for a composite). Nodes of base.Prefix may
// leave Prefix empty.
func Encode(cf lwm2m.ContentFormat, base Path, nodes []Node) ([]byte, error) {
	nodes = slices.Clone(nodes)
	for i := range nodes {
		if nodes[i].Prefix == "" && base.Prefix != "" {
			nodes[i].Prefix = base.Prefix
		}
		if base.Prefix != "" && nodes[i].Prefix != base.Prefix {
			return nil, fmt.Errorf("gateway: node of %q under %s", nodes[i].Prefix, base)
		}
	}
	order, g := group(nodes)
	if c, ok := senmlCodec(cf); ok {
		return encodeSenML(c, base, order, g, nodes)
	}
	if cf.Canonical() == lwm2m.FormatLwM2MCBOR {
		return encodeLwM2MCBOR(base, order, g)
	}
	c, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	if len(order) > 1 || len(order) == 1 && order[0] != base.Prefix {
		return nil, fmt.Errorf("gateway: %v cannot carry prefixes", cf)
	}
	return c.Encode(base.Path, g[base.Prefix])
}

func encodeSenML(c senml.Codec, base Path, order []string, g map[string][]lwm2m.Node, nodes []Node) ([]byte, error) {
	if len(order) == 1 {
		root := ""
		if order[0] != "" {
			root = "/" + order[0]
		}
		return c.WithRootPath(root).Encode(base.Path, g[order[0]])
	}
	// Several devices: one record per node with its full name and absolute
	// time, so base names and base times never leak across devices.
	var rs []senml.Record
	for _, n := range nodes {
		if n.Kind != lwm2m.KindValue {
			continue
		}
		root := ""
		if n.Prefix != "" {
			root = "/" + n.Prefix
		}
		b, err := c.WithRootPath(root).Encode(lwm2m.Root, []lwm2m.Node{n.Node})
		if err != nil {
			return nil, err
		}
		one, err := c.DecodeRecords(b)
		if err != nil || len(one) != 1 {
			return nil, fmt.Errorf("gateway: encode %s: %v", n.Path, err)
		}
		r := one[0]
		r.BaseTime, r.Time = 0, 0
		if n.HasTime {
			r.Time = n.Time
		}
		rs = append(rs, r)
	}
	return c.EncodeRecords(rs)
}

// encodeLwM2MCBOR encodes each device with the plain encoder and puts its
// prefix in front of every top-level key: ["d01", 3, 0] (GW §9, CBOR-11).
func encodeLwM2MCBOR(base Path, order []string, g map[string][]lwm2m.Node) ([]byte, error) {
	var body bytes.Buffer
	count := 0
	for _, pre := range order {
		enc, err := lwm2mcbor.Codec{}.Encode(base.Path, g[pre])
		if err != nil {
			return nil, err
		}
		n, rest, err := mapHead(enc)
		if err != nil {
			return nil, err
		}
		count += n
		for range n {
			var key any
			var val cbor.RawMessage
			kb := rest
			if rest, err = cbor.UnmarshalFirst(rest, &key); err != nil {
				return nil, err
			}
			kb = kb[:len(kb)-len(rest)]
			if rest, err = cbor.UnmarshalFirst(rest, &val); err != nil {
				return nil, err
			}
			if pre == "" {
				body.Write(kb)
			} else {
				ids := []any{pre}
				switch k := key.(type) {
				case uint64:
					ids = append(ids, k)
				case []any:
					ids = append(ids, k...)
				}
				b, err := cbor.Marshal(ids)
				if err != nil {
					return nil, err
				}
				body.Write(b)
			}
			body.Write(val)
		}
	}
	var out bytes.Buffer
	writeHead(&out, 5, uint64(count))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// mapHead reads a definite map header.
func mapHead(b []byte) (int, []byte, error) {
	if len(b) == 0 || b[0]>>5 != 5 {
		return 0, nil, errors.New("gateway: not a CBOR map")
	}
	switch info := b[0] & 0x1f; {
	case info < 24:
		return int(info), b[1:], nil
	case info == 24 && len(b) > 1:
		return int(b[1]), b[2:], nil
	case info == 25 && len(b) > 2:
		return int(b[1])<<8 | int(b[2]), b[3:], nil
	}
	return 0, nil, errors.New("gateway: CBOR map too large")
}

func writeHead(buf *bytes.Buffer, major byte, n uint64) {
	m := major << 5
	switch {
	case n < 24:
		buf.WriteByte(m | byte(n))
	case n <= math.MaxUint8:
		buf.Write([]byte{m | 24, byte(n)})
	default:
		buf.Write([]byte{m | 25, byte(n >> 8), byte(n)})
	}
}

// EncodePaths writes a Read- or Observe-Composite path list in a SenML
// format, prefixes in the names (GW §8.3.2).
func EncodePaths(cf lwm2m.ContentFormat, paths []Path) ([]byte, error) {
	c, ok := senmlCodec(cf)
	if !ok {
		return nil, fmt.Errorf("gateway: %v cannot carry a composite path list", cf)
	}
	if len(paths) == 0 {
		return nil, errors.New("gateway: empty path list")
	}
	rs := make([]senml.Record, len(paths))
	for i, p := range paths {
		rs[i].Name = p.String()
	}
	return c.EncodeRecords(rs)
}
