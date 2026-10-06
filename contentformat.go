package lwm2m

import "strconv"

// ContentFormat is a CoAP Content-Format number (Core §7.4, 1.2.2 §7.5).
type ContentFormat uint16

const (
	FormatText          ContentFormat = 0
	FormatLinkFormat    ContentFormat = 40
	FormatOpaque        ContentFormat = 42
	FormatJSONGeneric   ContentFormat = 50 // application/json: Zephyr labels OMA JSON with it (tolerance C7)
	FormatCBOR          ContentFormat = 60
	FormatSenMLJSON     ContentFormat = 110
	FormatSenMLCBOR     ContentFormat = 112
	FormatSenMLETCHJSON ContentFormat = 320
	FormatSenMLETCHCBOR ContentFormat = 322
	FormatTLV           ContentFormat = 11542
	FormatOMAJSON       ContentFormat = 11543
	FormatLwM2MCBOR     ContentFormat = 11544

	// Pre-IANA numbers still emitted by old clients (FMT-06), input only.
	FormatLegacyText    ContentFormat = 1541
	FormatLegacyTLV     ContentFormat = 1542
	FormatLegacyOMAJSON ContentFormat = 1543
)

var formatNames = map[ContentFormat]string{
	FormatText: "text/plain", FormatLinkFormat: "application/link-format",
	FormatOpaque: "application/octet-stream", FormatJSONGeneric: "application/json",
	FormatCBOR: "application/cbor", FormatSenMLJSON: "application/senml+json",
	FormatSenMLCBOR: "application/senml+cbor", FormatSenMLETCHJSON: "application/senml-etch+json",
	FormatSenMLETCHCBOR: "application/senml-etch+cbor", FormatTLV: "application/vnd.oma.lwm2m+tlv",
	FormatOMAJSON: "application/vnd.oma.lwm2m+json", FormatLwM2MCBOR: "application/vnd.oma.lwm2m+cbor",
}

func (f ContentFormat) String() string {
	if s, ok := formatNames[f]; ok {
		return s
	}
	return "ContentFormat(" + strconv.Itoa(int(f)) + ")"
}

// Canonical maps tolerated aliases to the format they actually carry.
func (f ContentFormat) Canonical() ContentFormat {
	switch f {
	case FormatLegacyText:
		return FormatText
	case FormatLegacyTLV:
		return FormatTLV
	case FormatLegacyOMAJSON, FormatJSONGeneric:
		return FormatOMAJSON
	}
	return f
}
