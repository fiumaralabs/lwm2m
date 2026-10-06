// Package codec defines the content-format codec contract and a registry
// that maps CoAP Content-Format numbers to codecs.
package codec

import (
	"errors"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
)

// Codec encodes and decodes LwM2M payloads (Core §7.4, 1.2.2 §7.5).
//
// base is the request path the payload belongs to (the Uri-Path of a Read
// response or Write request, or the root for Send/Composite payloads).
// Nodes are flattened leaves (see lwm2m.Node), always with absolute paths.
//
// Single-value formats (plain text, opaque, CBOR) require base to be a
// resource or resource-instance path and exactly one node.
type Codec interface {
	Format() lwm2m.ContentFormat
	Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error)
	// Decode uses s to type untyped values and to tell single from
	// multi-instance resources. s may be nil for self-describing formats;
	// then numeric types are inferred.
	Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error)
}

// ErrUnsupportedFormat is returned for a Content-Format with no codec.
var ErrUnsupportedFormat = errors.New("codec: unsupported content format")

var registry = map[lwm2m.ContentFormat]Codec{}

// Register adds c to the registry. Codec packages call it from init.
func Register(c Codec) { registry[c.Format()] = c }

// For returns the codec for f, resolving tolerated aliases (FMT-06, C7).
func For(f lwm2m.ContentFormat) (Codec, error) {
	if c, ok := registry[f.Canonical()]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %d", ErrUnsupportedFormat, f)
}
