// Package all registers every content-format codec with the codec registry.
package all

import (
	_ "github.com/fiumaralabs/lwm2m/codec/cbor"
	_ "github.com/fiumaralabs/lwm2m/codec/lwm2mcbor"
	_ "github.com/fiumaralabs/lwm2m/codec/omajson"
	_ "github.com/fiumaralabs/lwm2m/codec/opaque"
	_ "github.com/fiumaralabs/lwm2m/codec/senml"
	_ "github.com/fiumaralabs/lwm2m/codec/text"
	_ "github.com/fiumaralabs/lwm2m/codec/tlv"
)
