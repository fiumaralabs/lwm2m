package codec

// Pin the CBOR library used by the CBOR-based codecs so concurrent work on
// those packages shares one go.mod entry.
import _ "github.com/fxamacker/cbor/v2"
