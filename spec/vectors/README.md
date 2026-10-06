# LwM2M content-format golden vectors

Portable test vectors extracted (not invented) from the unit tests of permissively licensed projects, and from the examples in the OMA TS:

- **Leshan** `22bc753133829b91aa1f090e1e1179e6957940a0`, EPL-2.0 / BSD-3-Clause (`source` prefix `leshan/`)
- **Zephyr** `74b7173e9c929cf8eed570fb3df099c5514720c0`, Apache-2.0, `tests/net/lib/lwm2m/content_*` (`source` prefix `zephyr/`). These show the bytes a real Zephyr client sends or accepts, so they are the most useful.
- **Wakaama** `94ff56f77a2d24a5890e0e703809a47633aa7d4b`, EPL-2.0 / BSD-3-Clause (`source` prefix `wakaama/`)
- **OMA LwM2M TS 1.2.2** worked examples (`spec-examples.json`, cited by section)

**Licensing policy:** no vectors here are copied from projects under non-commercial or other restrictive licences (e.g. AVSystem Anjay / Anjay Lite). Behaviour notes about such clients below are our own descriptions of observed behaviour. Vectors for the formats they covered (LwM2M CBOR, SenML-ETCH, opaque, 1.2 attributes) are written by us and checked against those clients as live CI peers, not copied from their tests.

## File layout

One JSON file per format. Each file is a JSON array of vector objects.

## Vector schema

```jsonc
{
  "id": "tlv-zephyr-put-s32-1",         // unique within the file
  "source": "zephyr/tests/.../main.c:123", // file:line of the test or the literal
  "direction": "encode" | "decode" | "both",
  "path": "/3/0/1",                      // optional: request/target path the codec was given
  "content_format": 11542,               // CoAP content-format number
  "bytes_hex": "c8...",                  // lowercase hex, no separators (binary formats)
  "text": "...",                         // OR literal payload (text formats: plain text, JSON, link-format)
  "expected": [ /* nodes */ ] | "error" | { /* format-specific object, see below */ },
  "notes": "free text"
}
```

- `encode`: encoding `expected` must give exactly `bytes_hex`/`text`.
- `decode`: decoding the payload must give `expected`. `"expected": "error"` means the decoder must reject it.
- `both`: the vector round-trips.
- For JSON-text formats, the encoder output is compared byte-exact only when the source test compared strings. Otherwise `notes` says "semantic compare".

## Value model

`expected` is a list of **nodes**:

```jsonc
{ "path": "/3/0/0", "type": "string", "value": "Open Mobile Alliance" }
{ "path": "/3/0/6/0", "type": "integer", "value": 1 }      // resource instance
{ "path": "/3/0/13", "type": "time", "value": 1367491215 }
{ "path": "/3/0/1", "type": "integer", "value": 5, "time": 1367491215 } // optional SenML/JSON timestamp (seconds, may be float)
```

Every path is a full resource path (`/o/i/r`) or resource-instance path (`/o/i/r/ri`). Object or instance reads are flattened into their leaf resources. Multi-instance resources are always given as `/o/i/r/ri` leaves. An empty multi-instance resource or an empty instance can be written as `{ "path": "/o/i/r", "type": "multiple", "value": [] }` or `{ "path": "/o/i", "type": "instance", "value": [] }`.

| type       | JSON `value`                                         |
|------------|------------------------------------------------------|
| `string`   | JSON string (UTF-8)                                  |
| `integer`  | JSON number (signed 64-bit). Values beyond 2^53 are given as a decimal string |
| `unsigned` | JSON number, or a decimal string when larger than 2^53 |
| `float`    | JSON number. `"NaN"`, `"Infinity"` or `"-Infinity"` as strings when needed |
| `boolean`  | `true` / `false`                                     |
| `opaque`   | lowercase hex string (`""` for empty)                |
| `time`     | integer seconds since the Unix epoch                 |
| `objlnk`   | `"objectId:instanceId"`, e.g. `"65535:65535"`        |
| `corelnk`  | CoRE link-format string                              |
| `none`     | `null`: a resource with no value (rare, notes say why) |

Optional extra node/vector fields:
- `"prefix": "dev1"` on a node: LwM2M Gateway end-device prefix. `path` is then the path inside that end device.
- `"depth": n` on a `link_format.json` vector: the Discover `depth` query.

Formats with non-node results (link-format, paths, attributes) use format-specific `expected` objects, described in the section for each file below.

### Conventions that apply to all files

- **Placeholder paths in Zephyr vectors.** Zephyr's codec tests zero out `test_path`, so Zephyr single-value vectors carry a placeholder path, not a real one:
  - TLV uses `/0/0/170` or `/0/0/43707` (the 0xAA or 0xAABB resource id is part of the bytes).
  - Plain text uses `/0/0/0`.
  - CBOR (60) uses `""` and leaves out the top-level `path`.
  - SenML-CBOR and OMA JSON use the real `/65535/0/<rid>` test object.

  For single-value formats (plain text, CBOR), ignore the node path.
- **Types without a model.** For Zephyr vectors the type comes from the reader or writer call (`put_s8`, `get_float`, and so on), not from a model. For Leshan decode vectors the type comes from the object model the test loaded (Leshan's default models in `leshan-lwm2m-core/src/main/resources/models`, plus test objects 65 and 66 from LwM2M 1.0.1 figure 28).
- **Leshan root path.** Leshan decoder tests that run under several root paths were extracted with `rootPath=null` (`%%ROOTPATH%%` becomes `""`).
- **Computed bytes.** Some Leshan TLV inputs are built with `TlvEncoder` in the test, not written as a literal. Their hex was computed with the same rules, and `notes` says so.
- **Encode errors.** `"direction":"encode","expected":"error"` with no payload means the encoder must refuse the input described in `notes` (for example, a multi-instance resource in plain text).
- **Skipped cases.** Zephyr `*_nomem`, `*_nodata` buffer-size cases and `*_truncate` cases were skipped everywhere. They test the caller's buffer size, not the codec.

### Format-specific `expected` shapes

| File | Extra shapes |
|------|--------------|
| `tlv.json` | `{"tlv":[{"type":"RESOURCE_VALUE\|MULTIPLE_RESOURCE\|RESOURCE_INSTANCE\|OBJECT_INSTANCE","id":n,"value_hex":"..."\|"children":[...]}]}` for raw TLV-structure tests. `{"tlv_value":{"type":..,"value":..}}` for value-only tests (bytes are the TLV value, with no header). |
| `oma_json.json` | `{"json":{...}}` for raw JSON-document round-trips (no model). `{"paths":[...]}` for path-list encode/decode. |
| `senml_json.json`, `senml_cbor.json`, `senml_etch_json.json`, `senml_etch_cbor.json` | `{"senml":[records...]}` for raw SenML-record tests. `{"paths":[...]}` for path lists. |
| `link_format.json` | `{"links":[{"uri":"/3/0","attributes":[{"name":"ver","value":"1.1","quoted":true?}\|{"name":"obs"}]}]}`, keeping attribute order. |
| `path.json` | `{"object":3,"instance":0,"resource":1,"resource_instance":null}`, with `null` for absent levels. The root `/` is all null. |
| `attributes.json` | `{"attributes":[{"name":"pmin","value":10}\|{"name":"cancel"}]}`. Numbers are JSON numbers; a valueless attribute has no `value`. |

`content_format` is `null` in `path.json` and `attributes.json`. In `objlnk.json` it is `0` for vectors run through a plain-text codec and `null` for pure parse/format helpers (Wakaama `utils_textToObjLink`/`utils_objLinkToText`); `expected` is a node of type `objlnk` or `"error"`.

## Counts

| File | CF | Total | Zephyr | Leshan | Wakaama | OMA TS | Must fail (`"error"`) |
|------|----|------:|-------:|-------:|-----:|-----:|------:|
| `tlv.json` | 11542 | 86 | 28 | 58 | 0 | 0 | 8 |
| `plain_text.json` | 0 | 60 | 52 | 8 | 0 | 0 | 9 |
| `opaque.json` | 42 | 0 | 0 | 0 | 0 | 0 | 0 |
| `cbor.json` | 60 | 63 | 63 | 0 | 0 | 0 | 7 |
| `lwm2m_cbor.json` | 11544 | 0 | 0 | 0 | 0 | 0 | 0 |
| `senml_cbor.json` | 112 | 68 | 57 | 11 | 0 | 0 | 8 |
| `senml_json.json` | 110 | 37 | 0 | 37 | 0 | 0 | 2 |
| `senml_etch_cbor.json` | 322 | 0 | 0 | 0 | 0 | 0 | 0 |
| `senml_etch_json.json` | 320 | 0 | 0 | 0 | 0 | 0 | 0 |
| `oma_json.json` | 11543 | 77 | 48 | 29 | 0 | 0 | 10 |
| `link_format.json` | 40 | 96 | 17 | 79 | 0 | 0 | 31 |
| `path.json` | - | 26 | 17 | 9 | 0 | 0 | 0 |
| `attributes.json` | - | 75 | 0 | 75 | 0 | 0 | 47 |
| `objlnk.json` | 0 / - | 12 | 0 | 0 | 12 | 0 | 6 |
| `spec-examples.json` | mixed | 106 | 0 | 0 | 0 | 106 | 6 |
| **total** | | **706** | **282** | **306** | **12** | **106** | **134** |

Gaps: there are no permissively licensed test sources, so we must write our own vectors:

- **LwM2M CBOR (11544):** only the 9 TS examples in `spec-examples.json`.
- **SenML-ETCH JSON/CBOR (320/322):** none. The TS has no ETCH byte examples.
- **Opaque (42):** none.
- **1.2 attributes** (edge, con, hqmax) and **Discover depth:** none.
- **Bootstrap-Pack:** none.
- **Objlnk:** only Wakaama's helper tests.
- **SenML-JSON:** Zephyr has no unit test for it.
- **Leshan CBOR (60):** no unit tests.
- **Attribute parsing:** none in Zephyr or Wakaama.

## Sources

Zephyr, `tests/net/lib/lwm2m/`:
- `content_oma_tlv`, `content_plain_text`, `content_raw_cbor`, `content_senml_cbor`, `content_json`, `content_link_format`
- path strings from `observation`

Leshan, `leshan-lwm2m-core/src/test/java/org/eclipse/leshan/core/`:
- `node/codec/LwM2mNodeDecoderTest`, `LwM2mNodeEncoderTest`
- `tlv/*`, `json/*`, `senml/**`
- `link/**`
- `node/LwM2mPathTest`, `request/WriteAttributesRequestTest`

Leshan client: `leshan-lwm2m-client/src/test/.../util/LinkFormatHelperTest`.

Wakaama: `tests/core_convert_numbers_test.c` (objlnk helpers).

## Known discrepancies between Leshan and Zephyr

These matter for a server that must accept what Zephyr sends.

### TLV
- **Floats:** Zephyr always writes an 8-byte double. Leshan writes 8 bytes for a Double and 4 for a Float. Both decoders accept 4 or 8 bytes.
- **Integers:** both use the smallest of 1, 2, 4 or 8 bytes (never 3), so Zephyr `put_s64(0)` gives `c1aa00`. Both reject any other length.
- **Booleans:** Zephyr's `get_bool` accepts a 1- or 2-byte value and treats nonzero as true. Leshan accepts only a 1-byte value.
- **Structure errors:** only Leshan tests these (duplicate ids, instance id that doesn't match the path, truncated TLVs, empty single resource). Zephyr ignores the resource id in the TLV when reading.

### Plain text
- **Opaque:** Leshan reads and writes opaque as base64 text. Zephyr's plain-text codec has no opaque support.
- **Booleans:** both write `1`/`0`. Leshan accepts only exactly `"0"` or `"1"`. Zephyr checks only the first character.
- **Floats:** both print whole numbers as `3.0`. Java may print `1.0E10` for large values, which Zephyr's parser likely doesn't accept (untested).
- **Empty payload:** Zephyr rejects it for numbers, booleans and objlnks, but decodes it as `""` for a string.
- **s64 overflow:** Zephyr rejects it; Leshan has no test.

### CBOR (60)
Leshan's side here comes from reading its codec code, not from tests.
- **Time:** Zephyr writes tag 0 with an RFC 3339 string (`"1970-01-01T00:00:01-00:00"`). Leshan writes tag 1 with an epoch integer. Zephyr's decoder also accepts an untagged value.
- **Objlnk:** Zephyr's encoder includes a trailing NUL in the text string (`64 303a30 00`), but its decoder expects none. Leshan's `"0:0"` has no NUL and would likely reject Zephyr's output, so a server should strip a trailing NUL.
- **Floats:** Zephyr always writes a double (`fb`). Leshan may write the shortest form.

### SenML-CBOR
- **Floats:** Zephyr always writes `fb`. Leshan's decoder accepts half floats (`f9`).
- **Objlnk:** Zephyr writes it under the text key `"vlo"` (not an integer label). Its decoder reads objlnk from `vs`, and also from `v` (`test_get_objlnk[0]`).
- **bn/n split:** Zephyr writes `bn="/65535/0/"` with `n="<rid>"` (or `n="10/0"`). Leshan puts the full path in `bn` with no `n` for single resources.
- **Timestamps:** Zephyr sends `bt` once as a plain uint, then relative `t`. Leshan repeats `bt` on every record as tag 4 (a decimal fraction).
- **Empty payload:** Zephyr rejects it. Leshan decodes an empty payload or `80` as an empty node.
- **Zephyr test bug:** `test_get_opaque` (main.c:1624) actually sends `vs` "test_opaque" for resource 4. The vector follows the bytes.

### OMA JSON and SenML-JSON
- **bn/n split:** same as SenML-CBOR. Zephyr writes `bn="/65535/0/"` plus `n`. Leshan uses a full-path `bn` for single resources.
- **Zephyr content format:** Zephyr's test sends Content-Format option 50 (`application/json`), but the payload is OMA JSON. The vectors record 11543.
- **Integers:** Zephyr writes the full int64 range. Decode integers exactly, without going through a float64.
- **Timestamps:**
  - Leshan's OMA JSON encoder writes an absolute `t` per entry with no `bt`.
  - Leshan's SenML encoder repeats `bn` with `bt` per timestamp.
  - Leshan's decoders accept `bt` plus relative `t`, fractional seconds and exponent notation (`1.638435E9`).
- **Other Leshan quirks:**
  - It accepts a trailing comma in one SenML literal.
  - Its OMA JSON decoder turns a bare `23` for an unknown object into a float, but its SenML decoder turns it into an integer.
  - `vd` is unpadded base64url.

### Link format
- **Bootstrap root link:** Zephyr writes `</>;lwm2m="1.0"` (quoted). Leshan writes `lwm2m=1.0` (unquoted).
- **Register root link:** Zephyr writes `</>;ct=11543` with no `rt`. Leshan writes `</>;rt="oma.lwm2m"`, adding `ct=11542` or a quoted list `ct="11542 11543 42"`.
- **ver:** Zephyr writes `</65535>;ver=1.1` followed by the instance links. Leshan emits `ver` only when it isn't 1.0. Both write `ver` unquoted, and Leshan's parser accepts it either way.
- **Bootstrap-Discover:** Zephyr writes `</0/0>;ssid=101` with no `uri`. Leshan writes `ssid` plus a quoted `uri`.
- **Discover:** Zephyr carries inherited instance attributes onto resource links (`</65535/0/0>;pmin=5;pmax=200`).
- **Escapes:** Leshan only unescapes `\"` inside a quoted-string.
- **Valueless pmin:** accepted in a Write-Attributes query, but rejected in a link (`</3/0/11>;pmin`).

### Paths
- Zephyr's `lwm2m_string_to_path` skips any non-digit, so it accepts a missing leading `/` and doubled or trailing slashes. Leshan's tests only use the canonical form. Neither side tests invalid paths.

## Discrepancies in the AVSystem and Wakaama sources

### LwM2M CBOR (11544)
- **Encoder layout:** Anjay puts the path below the request path in an array key (`{[13, 26]: {1: 42, 2: 21}}`) and uses nested int keys only at the root. Anjay Lite never uses array keys: one map per level from the object id (`{3: {3: {3: 25}}}`). Both write only indefinite-length maps; an empty Anjay Lite read is `bfff`. A server decoder must accept definite and indefinite maps, array keys, nested int keys and mixes of them.
- **Both decoders accept** indefinite byte/text strings, half floats, tag-4 decimal fractions (as doubles) and `null`.
- **Floats:** both encoders write 8-byte `fb` doubles.
- **Objlnk:** text string `"oid:iid"` with no trailing NUL (unlike Zephyr's CBOR 60).
- **Time:** Anjay Lite writes tag 1 + integer (`c1 03`).
- **Chunked text (Anjay Lite):** external-data strings go out as indefinite text, chunked to the buffer, including empty `60` chunks. Accept those.
- **Errors (Anjay only):** ids > 65535, paths outside the base path, non-map top level, float or text keys, empty array keys, truncated input. Both reject paths deeper than 4 levels.
- **Test comments vs bytes:** Anjay `null_and_int`, Anjay Lite `path_too_long_1` and Anjay `resource_instances_nested_maps` have comments that don't match their bytes. The vectors follow the bytes.

### Composite operations (Anjay Lite)
- **Read-Composite:** a missing, write-only or Security/OSCORE path is silently skipped. The reply is 2.05, possibly with an empty array `80`; it is never 4.04. A path given twice, or `/` next to other paths, is answered more than once. More paths than the client's limit gets 5.00.
- **Observe-Composite:** `/` is refused with 4.05. Any rejected path rolls back the whole request. A record carrying a value gets 4.00. Cancel-Observe-Composite matches by token and ignores the payload.

### SenML-ETCH and Bootstrap-Pack
- **Deleting a resource instance:** Anjay uses `v: null`. Anjay Lite treats a record with no value the same, and answers 4.00 to a null on a resource (not instance) path.
- **Anjay Lite** answers Read with Accept 322 in ETCH-CBOR laid out like plain SenML. Observe-Composite accepts 322 and 112, rejects 110 with 4.15, and refuses path lists carrying values or the root `/`.
- **Bootstrap-Pack Accept:** Anjay requests `/bspack` with Accept 112; Anjay Lite sends Accept 322. A server should answer either.

### Opaque (42) and objlnk
- Anjay rejects reading a non-opaque resource or an instance as CF 42 (4.06). An empty payload decodes as opaque `""`.
- **Objlnk text:** Anjay, Anjay Lite and Wakaama are all strict: no whitespace, both halves required, each part ≤ 65535.

### Attributes (1.2) and Discover
- **Empty value:** Anjay Lite accepts `pmax=` / `st=` as unset; Leshan and Anjay reject it.
- **Negative step:** Anjay Lite decodes `st=-0.8`; Leshan rejects `st=-6`.
- **pmin > pmax:** Anjay Lite accepts `pmin=10&pmax=5`; Leshan rejects it. Anjay Lite rejects `epmin == epmax`.
- **Anjay Lite validation:** edge and con must be 0 or 1. edge is allowed only on booleans. lt, gt, st and edge are not allowed in Observe-Composite. lt and edge are rejected on object/instance paths.
- **depth:** both AVSystem stacks accept 0-3 and reject 4. Anjay ignores depth for 1.0/1.1 servers and returns 4.02 for depth on Read. Without depth, Discover on an object or instance leaves out resource instances; Discover on a multi-instance resource lists them. An empty multi-instance resource shows `dim=0`.
- **Inherited attributes:** Anjay Lite links show only the attributes set at that level (Zephyr copies inherited ones down).
- **Bootstrap-Discover:** Anjay Lite writes `</>;lwm2m=1.2` unquoted (like Leshan, unlike Zephyr), lists an object with no instances as `</1>`, and gives OSCORE instances only `ssid`.
- **Numbers in links:** Anjay Lite writes `lt=4.2e+21` and integral values without decimals (`gt=0`). Anjay orders 1.2 attributes dim, pmin, pmax, epmin, epmax, hqmax, lt, edge.
