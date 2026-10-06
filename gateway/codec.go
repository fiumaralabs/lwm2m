package gateway

import (
	"errors"
	"fmt"
	"slices"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	_ "github.com/fiumaralabs/lwm2m/codec/lwm2mcbor"
	"github.com/fiumaralabs/lwm2m/codec/senml"
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

// named reports whether cf carries prefixes in its names (GW §9).
func named(cf lwm2m.ContentFormat) bool {
	_, ok := senmlCodec(cf)
	return ok || cf.Canonical() == lwm2m.FormatLwM2MCBOR
}

// Decode decodes a payload whose request path was base. Names may carry
// prefixes; a name without one inside a prefixed request belongs to that
// device (tolerated: the prefix is mandatory in names, GW §9).
func Decode(cf lwm2m.ContentFormat, base Path, data []byte, s lwm2m.Schema) ([]Node, error) {
	c, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	out, err := c.Decode(base.Path, data, s)
	if err != nil {
		return nil, err
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

// Encode encodes nodes for a request on base (a Write, Create or
// Write-Composite body; root for a composite). Nodes of base.Prefix may
// leave Prefix empty. Formats without names (TLV, text, opaque, CBOR)
// carry one device's data under its prefixed request path.
func Encode(cf lwm2m.ContentFormat, base Path, nodes []Node) ([]byte, error) {
	nodes = slices.Clone(nodes)
	for i := range nodes {
		if nodes[i].Prefix == "" {
			nodes[i].Prefix = base.Prefix
		}
		if base.Prefix != "" && nodes[i].Prefix != base.Prefix {
			return nil, fmt.Errorf("gateway: node of %q under %s", nodes[i].Prefix, base)
		}
		if !named(cf) {
			if nodes[i].Prefix != base.Prefix {
				return nil, fmt.Errorf("gateway: %v cannot carry prefixes", cf)
			}
			nodes[i].Prefix = ""
		}
	}
	c, err := codec.For(cf)
	if err != nil {
		return nil, err
	}
	return c.Encode(base.Path, nodes)
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
