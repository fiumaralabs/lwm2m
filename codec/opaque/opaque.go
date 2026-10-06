// Package opaque implements the opaque content format (42,
// application/octet-stream), Core §7.4.2 (1.2.2 §7.5.2): the raw octets of
// one Opaque resource or resource instance.
package opaque

import (
	"errors"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
)

func init() { codec.Register(Codec{}) }

var errOpaque = errors.New("opaque")

// Codec is the opaque codec.
type Codec struct{}

func (Codec) Format() lwm2m.ContentFormat { return lwm2m.FormatOpaque }

func checkBase(base lwm2m.Path) error {
	if !base.IsResource() && !base.IsResourceInstance() {
		return fmt.Errorf("%w: needs a resource or resource-instance path, got %v", errOpaque, base)
	}
	return nil
}

// Decode returns the payload as one opaque value at base. A resource the
// schema types as anything but Opaque, or a multi-instance resource path, is
// refused. An empty payload is an empty opaque value.
func (Codec) Decode(base lwm2m.Path, data []byte, s lwm2m.Schema) ([]lwm2m.Node, error) {
	if err := checkBase(base); err != nil {
		return nil, err
	}
	if s != nil {
		if def, ok := s.Resource(base.Truncate(3)); ok {
			if def.Type != lwm2m.TypeOpaque {
				return nil, fmt.Errorf("%w: %v is %v, not opaque", errOpaque, base, def.Type)
			}
			if def.Multiple && base.IsResource() {
				return nil, fmt.Errorf("%w: %v is multi-instance", errOpaque, base)
			}
		}
	}
	return []lwm2m.Node{lwm2m.ValueNode(base, lwm2m.Opaque(append([]byte{}, data...)))}, nil
}

// Encode returns the bytes of the single opaque node at base.
func (Codec) Encode(base lwm2m.Path, nodes []lwm2m.Node) ([]byte, error) {
	if err := checkBase(base); err != nil {
		return nil, err
	}
	if len(nodes) != 1 || nodes[0].Kind != lwm2m.KindValue || nodes[0].Path != base || nodes[0].Value.Type != lwm2m.TypeOpaque {
		return nil, fmt.Errorf("%w: needs exactly one opaque value at %v", errOpaque, base)
	}
	return nodes[0].Value.Bytes, nil
}
