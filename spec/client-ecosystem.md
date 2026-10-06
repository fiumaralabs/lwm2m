# LwM2M client ecosystem: interop profile and tolerance list

This file covers the clients a server meets in the field other than Zephyr (Zephyr is in [zephyr-client-profile.md](zephyr-client-profile.md)). Each entry records what the client sends, what it accepts, and how it reacts to server responses. §3 turns that into one tolerance list. §4 is the CI interop matrix.

**Policy (README §1).** We emit strictly per spec. We tolerate a client deviation unless the spec forbids accepting it. A tolerance never changes what we emit.

**Pinned sources.** Clones are in `scratchpad/src/`, shallow, read without running.

| Alias | Repo | Commit | Version |
|---|---|---|---|
| `wk` | eclipse-wakaama/wakaama | `94ff56f77a2d24a5890e0e703809a47633aa7d4b` | master |
| `aj` | AVSystem/Anjay | `fdd70854c46f676acda179ad3a1b760eceada4db` | 3.15.0 (CHANGELOG.md:3) |
| `avs` | AVSystem/avs_commons (under Anjay) | `e6c87eb5` | |
| `al` | AVSystem/Anjay-lite | `b33821b042637315c83c548e0bc8286d6dbf7316` | 3.0.2 (CHANGELOG.md:3) |
| `ls` | eclipse-leshan/leshan | `22bc753133829b91aa1f090e1e1179e6957940a0` | 2.0.0-SNAPSHOT |
| `ncs` | nrfconnect/sdk-nrf (sparse) | `52c2b7fa266bc4408876b454b55ba709711d048f` | |
| `zp` | zephyrproject-rtos/zephyr | `74b7173e9c929cf8eed570fb3df099c5514720c0` | |

Path prefixes in citations: `ls-client` = `leshan-lwm2m-client/src/main/java/org/eclipse/leshan/client`, `ls-core` = `leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core`, `ls-cf` = `leshan-tl-cf-client-coap/src/main/java/org/eclipse/leshan/transport/californium/client`.

**Markers.** **[U]** means unverified: inferred from code reading, not run, or from a secondary source. **[NA]** means the document was not publicly accessible. Spec citations are to OMA LwM2M TS 1.2.2 Core (`Core`) and Transport (`Tr`), or to the RFC named. A section number marked **§?** was not re-checked against the TS text and needs confirming against standards-1.2.md.

---

## 1. Capability matrix

| Client | LwM2M | Content formats (emit / accept) | Ops beyond 1.0 basics | Transports | Security | Queue mode | Bootstrap | Source |
|---|---|---|---|---|---|---|---|---|
| **Zephyr** | 1.0 (default) / 1.1, build-time | TLV, text, opaque, CBOR 60 (1.1), SenML-JSON/CBOR opt-in, OMA-JSON opt-in. No 11544, no ETCH | Composite Read/Write/Observe, Send (1.1) | UDP | PSK, X.509 (mbedTLS/offload), DTLS CID | yes, real RX-off | client-initiated, opt-in | see zephyr-client-profile.md |
| **Wakaama** | 1.1 (default) / 1.0, build-time; never 1.2 (wk wakaama.cmake:14,135-137; core/internals.h:129-138) | TLV, OMA-JSON, SenML-JSON, SenML-CBOR (all default ON); text, opaque; CBOR 60 **only in 1.0 builds** (inverted ifdef, wk data/data.c:714-718). No 11544, no ETCH (wk doc/wakaama_features.rst:73) | Send (1.1). **No FETCH/iPATCH, so no composite ops** (wk coap/er-coap-13/er-coap-13.h:102-107) | UDP only (wk doc/wakaama_features.rst:9-25) | tinydtls PSK only in the example; RPK callbacks commented out (wk transport/tinydtls/connection.c:245-249). No X.509, no OSCORE | `Q` sent, behaviour not implemented (wk doc/wakaama_features.rst:53) | client-initiated (`WAKAAMA_CLIENT_INITIATED_BOOTSTRAP`), BS-Read 1.1 | `wk` |
| **Anjay 3.x** | 1.0-1.2, negotiated down **on 4.12 only** (aj src/core/servers/anjay_register.c:526-595) | text, opaque, CBOR 60, TLV, SenML-JSON/CBOR, **LwM2M CBOR 11544 (1.2)**, OMA-JSON **output only**, **SenML-ETCH 320/322 input only** (aj src/core/io/anjay_dynamic.c:113-200) | Composite Read/Write/Observe, Send, Bootstrap-Pack, `edge/con/hqmax/epmin/epmax`, Discover `depth`, Gateway /25 (OFF by default) (aj CMakeLists.txt:273-286; README.md:66-110) | UDP, TCP (OSS); SMS and NIDD **commercial only** (aj include_public/anjay/anjay_config.h.in:255-279) | PSK, X.509, RPK depends on backend [U]; CID; TLS 1.3 over TCP only if mbedTLS has it; **no DTLS 1.3** [U]; EST, OSCORE, HSM **commercial only** (anjay_config.h.in:229-232,366-381,508) | yes, socket auto-close after MAX_TRANSMIT_WAIT (aj CMakeLists.txt:287) | client-initiated, `/bspack` first on 1.2, legacy 1.0 server-initiated | `aj` |
| **Anjay Lite 3.x** | **1.2** (or 1.1 if `ANJ_WITH_LWM2M12=OFF`); **never 1.0, no fallback** (al include_public/anj_internal/core.h:49-51) | text, opaque, CBOR 60, SenML-CBOR, **LwM2M CBOR 11544 (default)**, SenML-ETCH-CBOR 322; TLV **decode only**; no SenML-JSON, no OMA-JSON (al src/anj/io/io.c:41-56; cmake/anjay_lite-config.cmake:136) | Read/Write-Composite ON, **Observe-Composite OFF by default**, Send, Discover (al cmake/anjay_lite-config.cmake:44,82) | UDP only; TCP removed in 2.0 (al CHANGELOG.md:92-94). IPv6 OFF by default | PSK, certs (OFF by default); **CCM_8 suites only**; DTLS 1.2 only; CID if mbedTLS has it; no RPK; OSCORE commercial (al src/anj/compat/anj_mbedtls_dtls_socket.c:378-426; CHANGELOG.md:70,99) | `Q` + RX off [U] (al src/anj/core/reg_session.c:524-540) | client-initiated `/bs`; Bootstrap-Pack encoder exists but is never called | `al` |
| **Leshan client** | **1.1 hard-coded**, no 1.2, no fallback (ls-client/engine/DefaultRegistrationEngine.java:234,241; ls-core/LwM2m.java:28-30) | text, opaque, CBOR 60, SenML-JSON/CBOR, TLV, OMA-JSON; 1542/1543 with `-ocf`. No 11544, no ETCH (ls-core/node/codec/DefaultLwM2mEncoder.java:69-93) | Composite Read/Write/Observe, Send, BS-Discover/Read. No Bootstrap-Pack. Attributes up to `epmin/epmax`, no `edge/con/hqmax` | UDP (Californium), UDP/TCP/TLS (java-coap, "experimental") (ls JavaCoapsTcpClientEndpointsProvider.java:59-77) | PSK, RPK, X.509 (DTLS 1.2, Scandium); CID optional, off by default; OSCORE experimental, Californium only; EST enum only (ls-core/SecurityMode.java:22) | nominal: adds `Q`, DTLS client-only (DefaultRegistrationEngine.java:99) | client-initiated; BS-Read on /1, /2 only | `ls` |
| **NCS lwm2m_client (nRF91)** | Zephyr engine; sample defaults to **1.0**, 1.1 via overlay (ncs samples/cellular/lwm2m_client/overlay-lwm2m-1.1.conf:1-7) | as Zephyr; sample: TLV, text, opaque; 1.1 overlay adds SenML-CBOR | as Zephyr | UDP, IPv4; sockets offloaded, **DTLS in the modem** (ncs prj.conf:20-24,47) | PSK, X.509, NoSec; **no RPK** (ncs subsys/net/lib/lwm2m_client_utils/lwm2m/lwm2m_security.c:39-43); CID on (prj.conf:116-122) | **on**, 30 s uptime (prj.conf:114,130) | off by default; Leshan/AVSystem BS overlays | `ncs` |
| **Nordic LwM2M carrier lib** | not stated publicly; 1.1 inferred from Send/Mute Send in 3.5.0 [U] (ncs doc/nrf/libraries/bin/lwm2m_carrier/CHANGELOG.rst:185,229) | not documented [NA] | Send (3.5.0+) | **U or N (non-IP)** (lib/bin/lwm2m_carrier/include/lwm2m_carrier.h:538-540) | PSK via sec tags 25-28 (requirements.rst:44-48) | default y (Kconfig:236-238) | factory / smartcard | closed binary, v3.7.0 |
| **Quectel BG95/BG77/BG600L (and BG96 [U])** | 1.0 default, 1.1 selectable (`AT+QLWCFG="version"`) | not documented [NA] | not documented | UDP, SMS, **non-IP**; bindings `U UQ S SQ US UQS N`, plus `UN` in 1.1 | PSK, Certificate, NoSec; no RPK | yes | factory and client-initiated; **no server-initiated** | Quectel BG95/BG77/BG600L LwM2M AN V1.0 §2.1.1, Tables 4-8, p.10-34 |
| **Quectel BC66** | **1.0 only** | not documented | — | UDP (NB-IoT) | PSK, NoSec | UDP+Queue via `AT+QLWUPDATE` | factory, client- and **server-initiated** | Quectel BC66 LwM2M AN V2.1 §1, §3.1.1, §4.2.1 |
| **u-blox SARA-R4/R5** | believed 1.0 (R4), 1.1 later (R5) [NA: UBX-18068860] | host side uses OMA-JSON (§31.1); wire formats [NA] | — | UDP [U] | PSK (incl. root-of-trust derived), session resumption, NAT re-handshake timer | PSM-related options | per MNO profile | SARA-R5 AT manual UBX-19047455 R10 §31, App. C |
| **Sierra HL78xx** | 1.0.2 baseline; Send "starting LwM2M v1.1 support" | text, link, opaque, TLV, OMA-JSON (`+DMAPPDATA`); no SenML | Send (newer FW) | not documented | not documented | not documented | `+DMSESSION 3` | HL78xx AT Cmd Ref 41111821 Rev.20 §14, §14.5 |
| **Telit ME910C1/ME310G1** | [NA] | [NA] | [NA] | [NA] | PSK (`#LWM2MSTS` on PSK mismatch) | [NA] | factory → OneEdge | ME910C1 AT Ref 80529ST10815A Rev.9 §3.20; LwM2M AT guide 80529ST10974A [NA] |

**What no surveyed client implements.** MQTT and HTTP bindings (1.2 Tr §7/§8 §?), DTLS 1.3, and EST/OSCORE in open source (Leshan's OSCORE is experimental). Our server's support for these cannot be interop-tested against an open-source peer. Use Leshan *server* parity or self-tests instead (§4.3).

---

## 2. Per-client profiles

### 2.1 Eclipse Wakaama (`wk`)

**Register** (wk core/registration.c:155-241,776-786)
- `CON POST /rd`, 4-byte token derived from MID and time, CF 40.
- Uri-Query in fixed order: `lwm2m=1.1|1.0`, `ep`, `sms` (if set), `b`, `Q`, `lt`.
- `lt` comes from `/1/x/1` and is omitted if 0. The example default is 300 (wk examples/client/common/lwm2mclient.c:837).
- `b`:
  - 1.1: emitted whenever the Binding contains U/T/S/N, with `Q` as a separate parameter.
  - 1.0: always emitted, as one of `U`, `UQ`, `S`, `SQ`, `US`, `UQS`. Any other binding aborts registration (registration.c:172-197).
- **No percent-encoding of query values** (wk coap/er-coap-13/er-coap-13.c:1321-1345). Endpoint names containing `&` break.
- Payload is `</>;rt="oma.lwm2m";ct=110,</1>;ver=1.1,</1/0>,</2/0>,</3/0>,…` (wk core/objects.c:876-990).
  - `ct` is a **single value**: 112, else 110, else 11543 (wk core/internals.h:80-92).
  - Objects are sorted. `ver` appears only when non-zero. No resources. /0 is excluded.
  - **Bug:** /21 is excluded from the length computation but not from the output (objects.c:807-809 vs :913). Registration with an OSCORE object fails locally [U].
  - **Alt-path quirk:** the root link becomes `</alt>`, but object links carry no prefix. Incoming requests must carry the prefix (objects.c:755-777; core/uri.c:131-148).
- Registration ID: all Location-Path segments are joined with `/`, with no length or count limit, then re-split on Update (registration.c:668; er-coap-13.c:1243-1267).
- Payloads larger than the block size (1024) go out Block1 from the first packet. The client handles 2.31 and 4.13 with Size1 (wk coap/transaction.c:497-515; core/packet.c:772-844).

**Update** (registration.c:867-931)
- **Never sends any Uri-Query, even after `/1/x/1` is written.** A lifetime change by the server is never confirmed with `lt=`.
- A payload is sent only after Create/Delete or after an object is added or removed. **That Update carries no Content-Format option.**
- Timing: at `lt − 93 s` (MAX_TRANSMIT_WAIT), or at `lt/2` if `lt < 93` (registration.c:2083-2108).
- Execute on `/1/x/8` (Update Trigger), `/1/x/4` (Disable) and `/1/x/9` returns 2.04 in the example object, but **the core does nothing** (wk examples/client/common/object_server.c:669-677).

**Reaction to server responses**
| Situation | Behaviour | Cite |
|---|---|---|
| Register 2.01 | registered | registration.c:649-713 |
| Register 4.00 / 4.03 / 4.04 / 4.12 / timeout, 1.0 build | REG_FAILED immediately | same |
| same, 1.1 build | retry with `/1/x/18 × 2^(n−1)` (default 60 s), `/1/x/17` times (default 5); then `/1/x/20 × /1/x/19`; then bootstrap if `/1/x/16` | registration.c:530-628 |
| **Update non-2.04, including 4.04** | **REG_FAILED for that server, no re-Register.** Once all servers fail: bootstrap, or `lwm2m_step` returns 5.03 and **the example exits** | registration.c:849-858; core/liblwm2m.c:425,476-483; lwm2mclient.c:1226-1237 |
| Deregister response | ignored | registration.c:1133-1150 |
| **4.01 on any request** | **resends the identical message (same MID)** up to MAX_RETRANSMIT | coap/transaction.c:327-332 |
| CoAP retransmit | 2 s doubling, 4 retries, **no ACK_RANDOM_FACTOR jitter** | transaction.c:396-428 |

The 4.04-on-Update behaviour matters for C2 in the README. Wakaama does **not** re-register on 4.04. A server that answers 4.04 for a stale registration effectively kills a Wakaama client until its application restarts it.

**Server-initiated operations** (wk core/packet.c:511-685; core/management.c:163-402)
- Requests from an unknown session, from an unregistered server, `DELETE /` on a DM server, or POST /bs outside bootstrap get **no response at all**.
- **FETCH/iPATCH (codes above 4) get an empty ACK and then nothing** (packet.c:511,726-733). The server must not wait for a separate response. Do not send composite operations to a Wakaama client; it registers without advertising them, and 1.1 gives no way to discover that.
- Formats:
  - No CF on PUT/POST means TLV is assumed.
  - **An unknown CF is mapped to text/plain**, so 4.15 is never returned (core/utils.c:681-725).
  - Accept: only the first 2 values are parsed. No match gives 4.06.
  - No Accept: a single resource is returned as text/plain; anything else as SenML-CBOR > SenML-JSON > JSON > TLV, by what was compiled. **The example answers in SenML-JSON** (utils.c:832-846).
- **POST `/o/i/r` with a CF other than 0 is a partial Write, not an Execute** (management.c:299-342).
- Empty Uri-Path segments are tolerated, so `/3/0/` is treated as `/3/0` (core/uri.c:95-223).
- Any operation on /0 returns **4.01**.
- Create: an existing instance gives **4.06**, not 4.00. Success is 2.01 with `Location-Path o, i`, followed by a full Update.
- Discover without a handler gives **5.01**. Output over 1024 bytes gives 5.00 (core/objects.c:671; core/discover.c:21).
- Discover: no `ver`, no `ssid`, attributes only for this server.
- Observe:
  - **Notifications are always NON** (core/observe.c:803). There is no `con` attribute.
  - Observe=0 requires a non-empty token, else 4.00.
  - An RST on the last notification cancels the observation.
- Write-Attributes (management.c:55-161):
  - Only `pmin pmax gt lt st`. **`epmin`, `epmax`, `con`, `hqmax`, `edge` give 4.00.**
  - Floats without exponents. Keys are case-sensitive.
  - Attributes are per exact URI and **not inherited**.
  - **Bug:** a second Write-Attributes does not activate newly added attributes (observe.c:403-426) [U].
- Send: `CON POST /dp` with a **0-length token**, no Block1. **Mute Send defaults to true when `/1/x/23` is missing** (observe.c:849-981).
- Size: the JSON, SenML-JSON and SenML-CBOR encoders use fixed 1024-byte buffers, so a larger Read gives 5.00 even though Block2 exists (data/senml_cbor.c:28 etc.). Packets over 2048 bytes get 4.13.

**Encoder quirks**
- Text objlnk writes `:` at a fixed index, so the output is corrupt unless the object id has 5 digits (wk data/data.c:94-116) [U].
- TLV floats are float32 unless the value overflows (data/tlv.c:46-66).
- SenML: `bn` on the first record only. **No `bt`/`t` ever.** CBOR uses the `"vlo"` string label (data/senml_cbor.c:30-47).
- Text floats always have `.0`. `inf` and `nan` are emitted literally.

**Bootstrap** (wk core/bootstrap.c)
- `POST /bs?ep=&pct=112|110|11542`. `pct` only in 1.1 builds.
- Only 2.04 is success. The session times out after EXCHANGE_LIFETIME with no further command.
- BS commands without a CF are treated as **text/plain**, unlike the DM path.
- A PUT that fails to parse gives **5.00**.
- `DELETE /` keeps the BS server's own Security instance and deletes everything else. The Device object is not protected.
- BS-Discover: `</>;lwm2m=1.1,…` with an unquoted version. A 1.0 build omits `</>`.
- BS-Read on objects other than 1 and 2 gives 4.00, where the spec suggests 4.01.
- Finish gives 4.06 on an invalid configuration.

### 2.2 AVSystem Anjay 3.15 (`aj`)

**Register** (aj src/core/servers/anjay_register.c:473-499,785-799; src/core/anjay_utils_core.c:315-367)
- `POST <server-URI path segments>/rd`. **The server URI's own path and query are prepended.**
- Query: `lwm2m=1.x&ep=&lt=&b=&Q`.
  - `lt` is always sent; the demo default is 86400 (aj demo/demo_args.c:60).
  - **`b` is omitted when the binding is exactly `U`.**
  - `Q` is bare, in 1.1 and later. 1.0 uses `b=UQ`.
  - No `sms` or `sn` in the OSS build.
- Payload (aj src/core/io/anjay_corelnk.c:53-97):
  - **No `</>` root link at all, so no `rt` and no `ct`.**
  - `</3>;ver=1.1,</3/0>`. **On a 1.0 registration `ver` is quoted, as in `ver="1.1"`.**
  - /0 excluded; /21 excluded per the comment citing Core 1.1 §6.2.1.
- **Version negotiation:** Anjay sends the maximum version first and steps down one minor version **only on 4.12**. 4.00 and 4.03 count as rejection, with no step-down (anjay_register.c:526-595).
  - So a server that rejects an unsupported `lwm2m=` with 4.00 strands Anjay.
  - README REG-04 already says 4.12, which is correct.
- Location-Path: every segment is stored and replayed (anjay_register.c:405-437). A Block2 response to Register is cancelled after the first block (639-646).

**Update and reconnect**
- Update at `expire − min(lt/2, MAX_TRANSMIT_WAIT)`, floor 1 s. With `lt=0`, no Updates.
- Only changed `lt`, `b` or payload (CF 40) is sent (anjay_register.c:40-160,802-847).
- **Create/Delete do not trigger an immediate Update** unless `update_immediately_on_dm_change` (aj include_public/anjay/core.h:556-568). The server's view of the instance list can lag until the next Update. Don't treat a Read on a just-created instance missing from the registration as an error.
- Any non-2.04 Update or a timeout leads to a **full re-Register** (anjay_register.c:849-913).
- A **new DTLS session that was not resumed leads to a re-Register**. A resumed session or CID leads to an Update (aj src/core/servers/anjay_connections.c:392-426). This is the opposite of Zephyr's C2.
- NoSec reconnects always re-Register [U].
- Register retry follows `/1/x/17-20`, defaults 5 / 60 s / 1 / 86400 (aj include_public/anjay/dm.h:1286-1292; src/core/servers/anjay_activate.c:176-235). Then bootstrap if `/1/x/16`.
- The client rebinds the same local port and prefers the last resolved IP (aj src/core/servers/anjay_connection_ip.c:255-290). UDP sockets are connected, so **replies must come from the IP:port the client sent to** [U].

**Server-initiated operations** (aj src/core/anjay_core.c:507-1061)
- Method mapping:
  - PUT without CF is **Write-Attributes**.
  - POST `/O/I` is a partial Write. POST `/O/I/R/RI` is a Write.
  - GET with Accept 40 is Discover.
- **Every Uri parse error gives 4.02 Bad Option**, not 4.00. This covers:
  - a **trailing slash or empty segment** (`/3/`)
  - IDs of 65535 or more
  - unknown query keys
  - duplicate attributes
  - negative pmin/pmax
  - `depth` outside 0-3
  - attributes on any method other than Write-Attributes/Observe
- **FETCH/iPATCH must carry no Uri-Path and no Uri-Query, or Anjay returns 4.02** (anjay_core.c:1005-1020; deps/avs_coap/src/avs_coap_ctx.c:432-476).
  - Composite operations always target root.
  - **Observe-Composite cannot carry attributes in the query.** 1.2 allows attributes in the Observe request (Core §?), so with Anjay set them through Write-Attributes beforehand.
- Formats when there is no Accept:
  - hierarchical data: TLV on 1.0, **SenML-CBOR on 1.1/1.2**
  - single resource: **text/plain in every version** (aj src/core/io/anjay_dynamic.c:243-279)
- Format errors:
  - a Write without CF is decoded as text/plain
  - unsupported Accept → 4.06
  - unsupported CF → 4.15
  - composite on a 1.0 registration → 4.05
  - non-composite on root → 4.00
- Observe:
  - **NON by default.** CON when `con=1`, `/1/x/26`, or config requires it, and at least one CON every 24 h (aj src/core/observe/anjay_observe_core.c:1696-1707,2138-2146).
  - The request token is reused.
  - Observations are cancelled on re-Register.
- Write-Attributes: a bare key clears it. `con` and `edge` accept only 0 or 1. NaN is rejected.
- Create: always 2.01 with `Location-Path O, I`. An existing IID gives 4.00, and so does a payload with more than one instance (aj src/core/dm/anjay_dm_create.c:42-145).
- Discover:
  - `depth` is honoured on 1.2 only.
  - `/O/I/R/RI` gives 4.05.
  - `ver` is quoted on 1.0.
  - `dim=` on multiple-instance resources (aj src/core/dm/anjay_discover.c:113-160).
- **PSK ciphersuite allowlist when `/0/x/16` is absent:** C0A8, C0A4, 00A8, 00A9. **0x00AE (PSK-AES128-CBC-SHA256) is not included** (aj src/core/anjay_utils_core.c:478-514; CHANGELOG.md:6-10). Minimum (D)TLS 1.2.

**Bootstrap** (aj src/core/anjay_bootstrap_core.c)
- On 1.2, Anjay first sends **`GET /bspack?ep=` with Accept 112**. Any non-2.05 response falls back to `POST /bs?ep=&pct=112` (lines 1243-1258,1398-1430).
- A 4.xx on `/bs` at 1.1+ triggers a retry as 1.0: `POST /bs?ep=` with no `pct` (1116-1136).
- No `lwm2m=` on bootstrap.
- Hold-off: exponential, capped at 20 s, jitter ×[1,1.5].
- Finish failure gives 4.06. BS-Read only on /1 and /2.
- BS-Discover: on 1.0 the body starts with `lwm2m="1.0"` and has no `</>`. On 1.1+ it starts with `</>;lwm2m=1.x`.
- Legacy 1.0 server-initiated bootstrap is accepted at any time when `minimum_version` is 1.0 (anjay_core.c:113-121).

**Changelog interop history** (aj CHANGELOG.md): :823 unquoted `ver` for 1.1; :1253 empty `Uri-Path: ''` accepted as root; :1386,:1516 Accept allowed on PUT/POST; :1363 `prefer_hierarchical_formats`; :2001 Update Location-Path validator relaxed.

### 2.3 AVSystem Anjay Lite 3.0.2 (`al`)

**Register** (al src/anj/coap/attributes.c:169-189; src/anj/core/server_register.c:134-155; tests/integration/README.md:55-58)
- `POST /rd?ep=&lt=&lwm2m=1.2&b=U[&Q]`.
  - **Query order is `ep, lt, lwm2m, b, sms, Q`, not the spec order.**
  - **`b=U` is always sent.**
- **The server URI path is ignored.** A URI with no port and no trailing `/` is rejected locally (al src/anj/core/core_utils.c:32-100).
- Payload: `</1>;ver=1.2,</1/0>,</3/0>`. **No `</>` root link.** /0 and /21 excluded (al src/anj/dm/dm_register.c:26-60).
- **Location-Path: at most 2 segments of at most 40 bytes each** (al cmake/anjay_lite-config.cmake:142-143).
  - A third segment fails decoding (al src/anj/coap/decode.c:376-381).
  - An over-long segment fails the Register (al src/anj/core/register.c:59-63).
- Buffers: 1200-byte messages, 1024-byte payloads, at most 15 options. Larger payloads go block-wise.
- **No version fallback.** A 4.12 is just a failure.

**Update and retry**
- Update at `now + max(lt − MAX_TRANSMIT_WAIT, lt/2)` (al src/anj/core/reg_session.c:49-72). Only changed `lt` or payload is sent.
- **Any failed exchange, whether an error code or a timeout, means disconnect and a full re-Register** (reg_session.c:510-568).
- Retry follows `/1/x/17-20`, defaults 5 / 60 / 86400 / 1 (al include_public/anj/defs.h:156-162). Then bootstrap if `/1/x/16`, otherwise the client is disabled.
- CoAP: ack_timeout 2 s, max_retransmit 4.

**Server-initiated operations** (al src/anj/coap/decode.c)
- **Malformed or unexpected requests are dropped silently, with no response** (reg_session.c:188-192,261-264). This covers:
  - **NON requests other than Execute** (decode.c:411-416)
  - **trailing or empty non-first Uri-Path segments** (decode.c:72-79)
  - more than 4 path segments
  - attribute options over 40 bytes
  - more than 15 options
  - **a payload marker with an empty payload** (decode.c:295)
  - POST to root or to `/O/I/R/RI`
  - Bootstrap operations on the DM connection
- Formats:
  - **No Accept gives LwM2M CBOR 11544, even for a single resource** (al src/anj/io/io.c:100-108).
  - **Unsupported Accept or CF gives 4.15, not 4.06** (io.c:66-97).
  - Text, opaque or CBOR for multi-record data gives 4.00.
- Attributes:
  - **Unknown keys are ignored.**
  - Matched by prefix; a bare key clears it.
  - `con`, `edge`, `hqmax` require 1.2 (al src/anj/coap/attributes.c:31-160).
- Observe: NON by default. CON per `con` or `/1/x/26`, and once every 24 h. Token reused. RST cancels (al src/anj/observe/notification.c:210-251).
- Create: 2.01 with `Location-Path O, I`.
- Bootstrap: `POST /bs?ep=&pct=112` (al src/anj/core/bootstrap.c:63-75).
  - **Retransmitted bootstrap requests are ignored, not re-answered** (al src/anj/core/server_bootstrap.c:127-170). A lost ACK on the BS server side means a timeout. Keep BS exchanges idempotent and retry with a new MID.
  - Finish gives 4.06 on failure.
  - Bootstrap is transactional since 3.0 (al CHANGELOG.md:39-42).

### 2.4 Eclipse Leshan client (`ls`)

**Register** (ls-cf/request/CoapRequestBuilder.java:88-134; ls-client/engine/DefaultRegistrationEngine.java:234-241)
- `POST /rd`, CF 40. Parameters: `ep`, `lt`, `sms` (never set), `lwm2m=1.1`, `b`, `Q`.
  - **Query parameters come from a `HashMap`, so their order is hash order** (CoapRequestBuilder.java:94-128). Hand-computed: `Q, b, lwm2m, lt, sms, ep` [U].
  - **`b` comes from Device `/3/0/16`, not from Server `/1/x/7`.** The demo always sends `b=UT` (leshan-demo-client MyDevice.java:205-207).
  - `Q` is bare.
- **`lwm2m=1.1` is always sent, even to a 1.0 server, with no fallback.**
- Endpoint mode `IF_NECESSARY` omits `ep` when it equals the PSK identity, the OSCORE sender ID, or, for X.509, **the CN of the Issuer DN**, which looks like a bug (ls-client/engine/DefaultClientEndpointNameProvider.java:79-120). A Register **without `ep`** is legal in 1.1 when the server can derive the name from credentials (Core §6.2.1 §?). We need to support that.
- Payload (ls-client/util/LinkFormatHelper.java:68-103):
  - `</>;rt="oma.lwm2m";ct="60 110 112 11542 11543",</1/0>,</3>;ver=1.2,</3/0>,…`
  - **`ct` is a quoted, space-separated list.** Formats mandatory for 1.1 (text, opaque, link) are left out; TLV is kept.
  - /0 and /21 skipped.
  - `ver` when the object has no instances or uses a non-default version. No resources.
  - Test reference: ls RegistrationTest.java:135.
- Registration ID:
  - Californium stores `getLocationString()`, i.e. `/rd/xyz` **plus any Location-Query**, and replays it verbatim as Uri-Path (ls-cf/request/LwM2mResponseBuilder.java:61; CoapRequestBuilder.java:140,165).
  - **Never send Location-Query.** The spec doesn't use it for /rd.
- **Success means exactly 2.01.** Update, Bootstrap-Request and Send need exactly 2.04, Deregister exactly 2.02 or 4.04 (LwM2mResponseBuilder.java:59-64,133-136; DefaultRegistrationEngine.java:307).

**Update and retry** (DefaultRegistrationEngine.java; ls-client/RegistrationUpdateHandler.java:49-110)
- The periodic Update is empty. Period = `lt − 247 s` (EXCHANGE_LIFETIME) when that is at least 30 s; shorter lifetimes are compressed into [1 s, 30 s]. **The demo with `lt=300` updates about every 53 s.**
- An object change sends the link list **without `ct`**, a format difference from Register. A lifetime change sends `lt=`. A `/3/0/16` change sends `b=`.
- `/1/x/4` Disable is not implemented and returns 4.04. `/1/x/9` deregisters and then bootstraps.
- Responses:
  - **Update non-2.04 means an immediate re-Register.**
  - A Register failure means bootstrap now if a BS server is configured, else a **fixed 10-minute retry with no backoff** (DefaultRegistrationEngine.java:517-519,564-636).
  - A timeout means one forced reconnect (new DTLS handshake, resumption by default), then one retry.
  - `/1/x/17-20` are ignored. Default pmin/pmax `/1/x/2-3` are never applied.
- Single server only (lowest SSID). Queue mode is nominal.

**Server-initiated operations** (ls-client/…/ObjectResource.java, RootResource.java)
- **No Accept means TLV for everything, a single resource included** (ls-core/request/ContentFormat.java:62; ObjectResource.java:299-310). Composite defaults to SenML-CBOR (RootResource.java:102).
- Unsupported Accept gives 4.06. No CF on PUT/POST gives 4.00. Unsupported CF gives 4.15.
- **PUT with any Uri-Query is Write-Attributes and the payload is ignored.** On an attribute parse error the client sends 4.00 and may then send a second response [U: missing `return`, ObjectResource.java:327-329].
- **POST on a resource with no CF or text/plain is an Execute** (ObjectResource.java:403-420).
- Discover requires Accept 40; without it, the request is handled as a Read (ObjectResource.java:146).
- Write-Attributes: `pmin pmax gt lt st epmin epmax`. No `edge`, `con` or `hqmax`. Checks pmin≤pmax, epmin≤epmax, lt<gt, lt+2·st<gt.
- Create: 2.01 with Location-Path only when the client picked the IID. **With a server-chosen IID there is no Location-Path** (ls-client/resource/ObjectEnabler.java:155-203).
- Notifications are NON with periodic CON (Californium ObserveLayer) [U].
- **Requests from a different IP:port than the registered server (NoSec) or a different DTLS session get 5.00** (ls-cf/CaliforniumClientEndpointsProvider.java:109-116; LwM2mClientCoapResource.java:46-54). The server must originate requests from the same socket the client registered to.

**Bootstrap** (ls-cf/bootstrap/BootstrapResource.java:51-82)
- `POST /bs?ep=&pct=112`. `pct` is always present.
- **Bootstrap-Finish is answered with an empty ACK, then a separate response. An error response is forced CON with a text body.** The server must handle a separate response to Finish.
- Finish validation requires /0, /1, /3 and `/3/0/16`. Failure gives 4.06.
- **BS-Discover reports `</>;lwm2m=1.0` although the client registers with 1.1** (LinkFormatHelper.java:107-109,179-181; BootstrapTest.java:386).

### 2.5 Nordic nRF91: NCS `lwm2m_client` sample and the carrier library

The NCS sample runs on the Zephyr engine, so everything in zephyr-client-profile.md applies. NCS-specific defaults (`ncs samples/cellular/lwm2m_client/`):
- **Lifetime 43200 s (12 h)**, Update every 5400 s (60 s early) (prj.conf:133-137).
- Queue mode on, 30 s uptime (prj.conf:114,130).
- **CID on with `STOP_POLLING_AT_IDLE`**: the socket is kept open while the NAT rebinds. The prj.conf comment says to use `SUSPEND_SOCKET_AT_IDLE` if the server lacks CID (prj.conf:116-122). Without server CID, every wake-up means a re-handshake.
- Session caching on (prj.conf:125).
- Endpoint `urn:imei:<15 digits>` (Kconfig:118-120; src/main.c:599-609). **PSK identity = endpoint name** (src/main.c:274-279).
- The IMEI is also used as `/3/0/2` (main.c:270-271).
- **DTLS terminates in the modem** (offloaded sockets, prj.conf:20-24,47). The cipher suites and the CID implementation are the modem firmware's, not mbedTLS's. CID persistence needs modem FW 1.3.5 or later (ncs subsys/net/lib/lwm2m_client_utils/Kconfig:277-285).
- **Writing Security keys puts the modem offline** (CFUN=4) to store them (lwm2m_security.c:46+). Expect a reconnect gap after Bootstrap-Finish or a Security write.
- CoAP ACK timeout 4 s, 15 s on NB-IoT (prj.conf:51; overlay-nbiot.conf:2). The server must tolerate slow ACKs and long separate-response gaps.
- 1.1 interop overlays add SenML-JSON, OMA-JSON and Portfolio (overlay-lwm2m-1.1-core-interop.conf:20-42).

Carrier library (closed; ncs doc/nrf/libraries/bin/lwm2m_carrier/):
- Operator chosen by SIM: Verizon, T-Mobile, LG U+, SoftBank, Bell (preliminary), or generic (lib/bin/lwm2m_carrier/Kconfig:46-130).
- **AT&T was removed in 3.4.0** (CHANGELOG.rst:214-215,309-319).
- Generic objects: 0-7, 10, 11, 19, 20. LG U+ uses 10250 (app_integration.rst:346-381; lwm2m_carrier.h:1058-1063).
- Session idle timeout 60 s closes DTLS unless CID is used (Kconfig:284-289). Lifetime 0 means the per-carrier factory value; the generic default is 1 h.
- **Changing server settings means a factory reset and re-bootstrap** (app_integration.rst:161).
- In Verizon mode the PSK identity is overwritten and **both modem DTLS sessions** are taken (CHANGELOG.rst:235-237; requirements.rst:66-70).

### 2.6 Quectel

**BG95/BG77/BG600L** (Qualcomm MDM9205 stack; BG96 shares it [U]). Source: *Quectel BG95&BG77&BG600L LwM2M Application Note V1.0*, https://sixfab.com/wp-content/uploads/2023/05/Quectel_BG95BG77BG600L_Series_LwM2M_Application_Note_V1.0.pdf
- **Endpoint name** is chosen by enum (Table 8, p.32):
  - `urn:imei:` (the registration default)
  - `urn:esn:`
  - `urn:meid:`
  - **`urn:imei-msisdn:<IMEI>-<MSISDN>` (the bootstrap default)**
  - `urn:imei-imsi:`
  - **`imei-imsi:<IMEI>-<IMSI>` with no `urn:`**
  - free-form
  - **The bootstrap endpoint name and the DM endpoint name differ by default.**
- **Bindings** (Table 7): `U UQ S SQ US UQS N`. The 1.1 example writes `/1/x/7="UN"` and `/1/x/22="U"` (p.17).
- **Retry** (p.20):
  - REG_RETRY 60 s ×2, max 480 s; then a 24 h hold-off.
  - **`REG_UPDATE_ON_RECONNECT=0`: no Update after an IP change.** The server keeps a stale address until the next Update.
  - `SESSION_TIMEOUT=60`: DTLS resumption after NAT idle.
- In PSM the client is shut down and registration can lapse (§2.4 p.14).
- Built-in profiles: Verizon, AT&T, T-Mobile, Telstra, LG U+ and DoCoMo, each with staging and production. SSIDs: 100 = BS, 102 = DM.
- `AT+QLWSVC="lifetime"` allows 0-86400 and sends an Update with the new `lt` (p.33-34).

**BC66** (MediaTek, NB-IoT, **1.0 only**). Source: *BC66 LwM2M AN V2.1*, https://forums.quectel.com/uploads/short-url/1cRZdhTyUnG6F8Md0DVim2fURSZ.pdf
- **Lifetime 20 s to 365 d.** The Update interval is non-linear: for `lt` over 300 s it is `250 + (lt − 300)·19/20`, so Updates come well before expiry.
- `AT+QLWUPDATE` can change the binding at runtime, giving an **Update with `b=`**.
- Security: PSK or NoSec only.
- Up to 15 custom objects; those support Read/Write/Execute/Observe only.
- **With a T-Mobile SIM**, the module auto-bootstraps to `bootp.iot.t-mobile.com:5584` with a self-derived PSK (§3.4).

**BC660K**: the LwM2M AN V1.2 needs a portal login [NA].

### 2.7 u-blox SARA-R4/R5

Source: *SARA-R5 AT commands manual UBX-19047455 R10*, §31 and App. C, https://docs.particle.io/assets/datasheets/SARA-R5_ATCommands_UBX-19047455.pdf. The LwM2M application note UBX-18068860 is [NA].
- The server set depends on `+UMNOPROF`. Profiles 0, 201 (GCF-PTCRB) and 199 (generic AT&T) disable LwM2M.
- **SSID 721 is the u-blox server** (uFOTA), present alongside the operator servers. Verizon profile SSIDs are 100, 101, 102, 1000 and 721 (App. C.2). Expect **multi-server clients where we are one of 2-4 servers.**
- `+ULWM2MCONFIG` options (§31.2.5.3) that affect the server:
  - `DTLS_NAT_timer`: re-handshake after NAT idle
  - **`reg_upd_at_PSM_exit`: full re-Register on PSM exit**
  - `reg_upd_after_DTLS_handshake`: Update after every handshake
  - `full_registration_after_fota`
  - `server_disabled` while roaming
  - `usec_psk`: PSK derived from the u-blox root of trust
- Verizon forces `connection_teardown_timer=60`; others use 90 s (§31.2.6.4).
- The version, objects, wire formats and endpoint format are [NA]. `urn:imei:` is believed [U].

### 2.8 Sierra Wireless / Semtech HL78xx

Source: *HL78xx AT Command Reference Guide 41111821 Rev.20*, ch.14, https://mutec.secure-jp.info/doc/HL78xx-AT_Command_Reference_Guide-Rev20.pdf
- There is an AirVantage (AVMS) client and a separate "carrier client" for AT&T, Verizon, SoftBank and LG U+ (§14).
- The baseline is OMA-TS-LightweightM2M-V1_0_2. `+DMAPPDATA` with no token triggers a 1.1 Send (§14.5).
- Formats: 0, 40, 42, 11542, 11543. No SenML. A 1.0-registered HL78 sending a 1.1 Send is a **version-mixing quirk** [U: whether it registers 1.1 in that firmware].
- Portfolio /16 carries the host identity for AT&T (§14.13).
- **Forum:** HL7800 "cannot connect to any generic LWM2M server", https://forum.sierrawireless.com/t/hl78xx-direct-lwm2m-support/16939 [U: may be firmware-dependent]. Treat HL78xx as operator/AirVantage-only.

### 2.9 Telit ME910C1/ME310G1

Source: *ME910C1 AT Commands Reference Guide 80529ST10815A Rev.9* §3.20, https://multitech.com/wp-content/uploads/Telit_ME910C1_AT_Commands_Guide_r9-1.pdf
- `AT#LWM2MENA` enables the client (default disabled). Factory configuration points at Telit OneEdge/deviceWISE.
- Objects 0, 1, 2 and 5 are not host-accessible.
- `#LWM2MSTS` switches the server or BS URI on PSK mismatch (§3.20.8).
- Version, formats, endpoint and lifetime are in the dedicated LwM2M AT guide 80529ST10974A [NA].

### 2.10 Operator requirements

| Operator | Mandated (public evidence) | Server-relevant consequences | Sources |
|---|---|---|---|
| **Verizon** (ThingSpace / OTADM, Nokia Motive) | LwM2M OTADM Reference Client Package through ODP (requirements not public). Factory bootstrap to Motive BS. **PSK identity `urn:imei-msisdn:<IMEI>-<MSISDN>`, PSK = SHA-256("com.vzwm2m.com")** (via Quectel). SSIDs 100 (BS), 101, 102 (DM), 1000. 60 s connection teardown. Custom object **10299 HostDevice**. Version: 1.0 implied by the Quectel default [U]. Carrier library mandatory for Verizon-certified nRF91 | Accept `urn:imei-msisdn:` endpoints. Expect TLV-only 1.0 clients with Verizon servers co-resident | Verizon OD Certification Process v32 §3.4.3.4 (https://opendevelopment.verizonwireless.com/content/dam/opendevelopment/pdf/OpenAccessReq/OD_Certification_Process_v32.pdf); Quectel BG95 AN §2.5.2; u-blox R10 App. C.2, §31.2.6.4; OMNA DDF.xml; Reqs-LTE_OTADM v43 covers OMA-DM only |
| **AT&T** | OMA-DM **or** LwM2M for host identity (ODIS/DHIR) and FOTA. **Object 10241 HostDeviceInfo** (Mandatory, Multiple), earlier interim Object 16 (Portfolio). Object 10308 Connectivity Extension. Details in confidential spec 13340 [NA]. Nordic dropped AT&T certification from the carrier library (3.4.0), and the u-blox generic AT&T profile disables LwM2M, so **AT&T LwM2M DM now looks legacy or optional** [U] | Load 10241, 10308 and 16 object definitions. Endpoint formats `urn:imei-imsi:` and `imei-imsi:` likely AT&T-driven [U] | AT&T Device Management Implementation Guide v2.3 §3.2, §4.3 (https://iotdevices.att.com/Uploaded_Docs/device_management_implementation_guide_20170606052838904.pdf); ncs CHANGELOG.rst:214-215 |
| **T-Mobile US** | BS server **`bootp.iot.t-mobile.com:5584`** (non-default port), PSK derived on the module, `urn:imei:` endpoint, example lifetime 900 s. Object list and version [NA] (the protocols guide returns 403) | Don't assume 5683/5684 for the BS URI | Quectel BC66 AN §3.4, §4.2.1; https://www.t-mobile.com/business/solutions/iot/device-certification/process |
| **Vodafone** | No public LwM2M device requirements found. OMNA lists Vodafone objects 10245-10249, 10479, 10481; whether they are used in DM is [U] | Load them from the registry | OMNA DDF.xml |
| **SoftBank** (via carrier libraries) | NB-IoT **non-IP binding N**, /19, /20, proprietary "divided FOTA" | NIDD binding is used in production | ncs lwm2m_carrier Kconfig, CHANGELOG |
| **LG U+** | Object 10250 App Data Container, service code, serial number = IMEI or 2DID | | ncs lwm2m_carrier.h:1058-1063 |
| **GSMA TS.34 v10.0** (cross-operator) | Randomized timers after outages, increasing back-off on rejection (§8.2.3 REQ_007), module SHOULD support OMA DM or LwM2M (5.8_REQ_001), efficiency policies manageable over LwM2M (7.2.2_REQ_001) | After an outage, registrations arrive spread out. **Answer with 5.03 + Max-Age rather than dropping** so clients back off cleanly | https://www.gsma.com/newsroom/wp-content/uploads//TS.34-v10.0-IoT-Device-Connection-Efficiency-Guidelines.pdf |

---

## 3. Tolerance list

The `#` column continues README §3: these rows become T-numbered entries there. "Forbidden?" asks whether the spec forbids the *server* from accepting the deviation. The handling column never changes what we emit.

### 3.1 Registration interface

| # | Quirk | Clients | Server handling | Forbidden? |
|---|---|---|---|---|
| T1 | Uri-Query order differs from the spec's table (hash order; `ep,lt,lwm2m,b,sms,Q`; `lwm2m` first) | Leshan, Anjay Lite, Wakaama, Anjay | Parse queries as an unordered set. Reject duplicate keys with 4.00 | No. RFC 7252 §5.10.1 sets no semantics for query order. Core §6.2.1 lists parameters, not an order |
| T2 | Bare `Q` (no `=`) | Zephyr 1.1, Wakaama, Anjay, Lite, Leshan | Accept `Q` and `Q=` the same way | No, this is the spec form in 1.1+ |
| T3 | Legacy 1.0 `b=` values `UQ`, `SQ`, `UQS`, `US` on a 1.0 registration; `UQ` in Server /1/x/7 | Wakaama 1.0, Anjay 1.0, Zephyr 1.0, Quectel, BC66 | On 1.0, parse queue from `b`. On 1.1+, also accept a trailing `Q` inside `b`, and treat it as queue mode | No for 1.0 (spec form). On 1.1, `Q` in `b` is a deviation but accepting it isn't forbidden |
| T4 | `b` omitted when the binding is U, or always `b=U` | Anjay (omits), Anjay Lite (always), Zephyr (omits) | Missing `b` → U | No. Optional, default U (Core §6.2.1 §?) |
| T5 | `b=UT` or `UN` multi-binding letters, including `N` | Leshan demo (`UT`), Quectel (`UN`), carrier lib (`N`) | Accept any combination of `U T S N` (and `M H` for 1.2). Store all of them; use Preferred Transport /1/x/22 if known | No |
| T6 | `ep` missing on Register | Leshan `IF_NECESSARY`/`NEVER` | Derive the endpoint from the authenticated identity (PSK id, cert CN or SAN, OSCORE id). If there's no identity (NoSec), reject with 4.00 | No. 1.1 allows omitting `ep` when the server can infer it (Core §6.2.1 §?) |
| T7 | Non-URN endpoint names (`imei-imsi:…`), long URNs (`urn:imei-msisdn:15-15`), free-form names; BS endpoint name ≠ DM endpoint name | Quectel, Verizon profile | Accept any UTF-8 percent-decoded string. Don't correlate BS and DM by endpoint name; correlate by credential | No. ep is "a unique name", not required to be a URN |
| T8 | Query values not percent-encoded | Wakaama | Accept raw bytes; percent-decode only well-formed `%XX` | No. RFC 3986 decoding of a value without escapes is the identity |
| T9 | `lwm2m=1.1` sent to a server that would prefer 1.0; no fallback | Leshan, Anjay Lite (1.2) | Serve each client at its declared version (README GEN-01). Return 4.12 **only** for versions we don't support at all | No |
| T10 | Version step-down only on 4.12; 4.00 is fatal | Anjay | For an unsupported `lwm2m=`, always answer **4.12** (README REG-04), never 4.00 | N/A (we comply) |
| T11 | Register payload with no `</>` root link (no `rt`, no `ct`) | Anjay, Anjay Lite, Zephyr | Assume the root `/`. Without `ct`, assume only the formats mandatory for the version (1.0: TLV, text, opaque; 1.1+: also SenML-CBOR or JSON per the SCR, §?) and fall back on 4.06/4.15 | No. The root link is required only when an alternate path is used |
| T12 | `ct` as a single unquoted value vs a quoted, space-separated list | Wakaama (`ct=110`), Leshan (`ct="60 110 …"`), Zephyr (`ct=112`) | Accept both. Split on whitespace | No. RFC 7252 §7.2.1 allows both |
| T13 | `ver="1.1"` quoted (on a 1.0 registration), or unquoted | Anjay 1.0 | Strip quotes before parsing `M.m` | No. RFC 6690 link-param values may be quoted strings |
| T14 | `ver` missing for an object whose version is not 1.0 (Zephyr hard-codes `/3` as 1.0 under 1.1) | Zephyr, Wakaama (only when non-zero) | No `ver` → the core version implied by the client's LwM2M version, or 1.0 for non-core objects. Don't reject when the object model mismatches; read with what the client sent | No |
| T15 | Object 21 (OSCORE) listed in the payload | Wakaama (bug, normally fails locally) | Ignore `/21` links | **Yes for the client** ("MUST NOT be part of the list", Core 1.1 §6.2.1). Ignoring it on input is allowed |
| T16 | Alt-path root `</alt>` but object links without the prefix | Wakaama | Address requests as `/alt/O/I`. Store links as-is | Deviation: the spec expects object links under the alternate path (§?). Accepting isn't forbidden |
| T17 | Update link payload with **no Content-Format** | Wakaama | If the Update has a payload and no CF, parse it as link-format | No. RFC 7252 §5.10.3 lets the receiver handle a missing CF. The spec says the payload is link-format |
| T18 | Update link payload without `ct`, after Register had `ct` | Leshan | Keep the Register `ct` set unless a new root link with `ct` arrives | No |
| T19 | Update never carries `lt` after the server wrote `/1/x/1` | Wakaama | Track the lifetime written via DM and apply it when the Write succeeds, not only on Update | No |
| T20 | Register/Update payload sent Block1 (size over 1024) | Wakaama, Anjay, Zephyr (opt-in) | Full RFC 7959 Block1 server on `/rd`, Size1, 4.13 with Block1 hint | No, server MUST (RFC 7959 per Tr §?) |
| T21 | Location-Path limits: at most 2 segments × 40 bytes (Lite); Zephyr stores only one ID segment of at most 32 bytes | Anjay Lite, Zephyr | Always `rd/<id>`, `<id>` ≤ 16 ASCII bytes. Never send Location-Query | N/A (we comply) |
| T22 | Location-Path and Location-Query replayed together as Uri-Path | Leshan | Never send Location-Query (see T21) | N/A |
| T23 | Exactly 2.01 / 2.04 / 2.02 required; other 2.xx count as failure | Leshan, Wakaama | Use exactly those codes | N/A (spec codes) |
| T24 | **Update after a new handshake or from a new address**: Update (Zephyr, u-blox `reg_upd_after_DTLS_handshake`) vs re-Register (Anjay new session, u-blox on PSM exit, Lite on any failure). Address changes with **no Update** (Quectel `REG_UPDATE_ON_RECONNECT=0`, NAT) | Zephyr, u-blox, Quectel, NCS CID | Route by DTLS CID or session, not by 5-tuple. Accept an Update from a new session when the authenticated identity matches (README C2). A new Register from a known endpoint replaces the old registration atomically. On NoSec, rebind only on an Update. | No. A re-Register supersedes (Core §6.2.1 §?) |
| T25 | **4.04 on Update**: Zephyr, Anjay, Lite and Leshan re-Register; **Wakaama marks the server failed and never re-registers** (the example exits) | Wakaama | Send 4.04 only when the registration is really gone (spec). Avoid spurious loss: keep registrations for `lt` + grace, and don't drop them on transient backend errors (use 5.03) | N/A |
| T26 | Very short lifetimes (BC66 ≥ 20 s, Zephyr 30 s) to very long ones (12 h NCS, 86400 Anjay); Updates far earlier than needed (BC66, Leshan `lt−247`); PSM gaps | BC66, Zephyr, NCS, Anjay, Leshan | No minimum lifetime enforced below the spec. Expire at `lt` + a configurable grace (default MAX_TRANSMIT_WAIT 93 s, adjustable for NB-IoT) | No. The grace period is server policy |
| T27 | Randomized and back-off registration storms after outages | all modems (TS.34) | Under load answer **5.03 + Max-Age**, don't drop | No |
| T28 | Multi-server clients with fixed SSIDs (100/101/102/1000/721/123) and a co-resident operator DM server | u-blox, Quectel, carrier lib, Wakaama example (123) | Make no assumption about SSID values. ACL (/2) and Write-Attributes are scoped by our SSID | No |
| T29 | Leshan sends `lt` and `b` changes on Update; Anjay and Lite send only what changed | Leshan, Anjay | Merge the Update into the stored registration; absent means unchanged | No (spec) |

### 3.2 Device management and information reporting

| # | Quirk | Clients | Server handling | Forbidden? |
|---|---|---|---|---|
| T30 | **Trailing slash or empty Uri-Path segment** answered 4.02 (Anjay) or **dropped silently** (Lite) | Anjay, Lite | Never emit empty segments. We comply already; listed so codec tests pin it | N/A |
| T31 | Composite FETCH/iPATCH with any Uri-Path or Uri-Query gives 4.02 | Anjay | Composite requests go to root with no query. Observe-Composite attributes go through Write-Attributes beforehand, not the query, when the client is Anjay (or always) | N/A. Choose the always-valid form |
| T32 | FETCH/iPATCH get an empty ACK and no response | Wakaama | Never send composite operations to clients that don't support them. Learn this per client: after an empty ACK and no separate response within EXCHANGE_LIFETIME, mark composite unsupported and fall back to individual Reads | No |
| T33 | NON requests dropped silently (except Execute) | Anjay Lite | Always send downlink requests as CON | N/A |
| T34 | Default format with no Accept: TLV (Leshan everywhere, 1.0 clients), text/plain for single resources (Anjay, Wakaama), SenML-JSON (Wakaama example), SenML-CBOR (Anjay 1.1+), **LwM2M CBOR 11544 even for single resources** (Lite) | all | Always set Accept explicitly (README FMT). Decoders accept every format we support, whatever was requested, keyed off the response CF | No. The CF of the response governs |
| T35 | Unsupported format code: 4.06 (Anjay, Leshan, Wakaama Accept), **4.15 for Accept** (Lite), unknown CF written as text (Wakaama) | Lite, Wakaama | Treat 4.06 and 4.15 the same way for format negotiation: retry with the next format from `ct` or the mandatory set | No |
| T36 | Error codes for invalid paths or values: **4.02** for parse errors (Anjay), 4.01 for /0 (Wakaama), 4.06 for "instance exists" (Wakaama), 5.01 or 5.00 for unimplemented ops (Wakaama, Leshan), 5.00 for "unknown server" (Leshan) | Anjay, Wakaama, Leshan | Map any 4.xx or 5.xx to a failed operation, and keep the raw code in the API. Don't branch logic on the specific code except 4.04, 4.05, 4.06, 4.15 and 4.13 | No |
| T37 | Create response with no Location-Path | Zephyr, Leshan (server-chosen IID) | Always include the IID in the Create payload. Location-Path optional (README C9) | No |
| T38 | Create does not trigger an Update; the registration's object list lags | Anjay (default), Wakaama and Leshan do trigger | Treat the Create 2.01 as authoritative and update the registry view locally | No |
| T39 | POST on a resource with text/plain or no CF = Execute; POST with another CF = partial Write | Leshan, Wakaama, Lite | Execute: POST with no CF and an optional text/plain-like argument list. Partial Write on a resource: send a hierarchical CF (TLV/SenML) | N/A (spec-compatible emission) |
| T40 | PUT with any Uri-Query = Write-Attributes, payload ignored; PUT without CF = Write-Attributes (Anjay) | Leshan, Wakaama, Anjay | Write-Attributes: PUT, query only, no payload, no CF. Write: never with a query | N/A |
| T41 | Attribute support: Wakaama rejects `epmin/epmax/edge/con/hqmax` with 4.00; Leshan rejects `edge/con/hqmax`; Lite ignores unknown keys silently; Anjay rejects unknown keys with 4.02 | all | Send only attributes valid for the client's registered version. On 4.00 or 4.02, retry without the 1.1/1.2 attributes and report the downgrade. Don't assume a 2.04 from Lite means the attribute took effect | No |
| T42 | Attributes not inherited (Wakaama per-URI watcher); a second Write-Attributes doesn't activate new keys (Wakaama bug) | Wakaama | Set attributes on the exact path observed, all in one Write-Attributes, before the Observe | No |
| T43 | Notifications NON-only (Wakaama) or NON by default (Anjay, Lite, Leshan) | all except Zephyr (CON-only) | Accept CON and NON notifications. RST unknown tokens. Never require CON unless `con=1` was set and the client is 1.2 | No (RFC 7641 §4.5) |
| T44 | Send with a **0-length token** (Wakaama); Send in SenML-CBOR or JSON from a 1.0-registered client (HL78 [U]) | Wakaama, HL78xx | Accept any token length 0-8. Accept Send from any registered client whose payload decodes, whatever its registered version; log the version mismatch | 0-length token: no (RFC 7252 §5.3.1). Send on a 1.0 registration: the 1.0 TS has no Send; accepting isn't forbidden, so tolerate |
| T45 | Request replies must come from the registered IP:port / same DTLS session (Leshan 5.00, Anjay connected socket) | Leshan, Anjay | Send downlink from the same listening socket that received the Register (no per-request ephemeral sockets) | N/A |
| T46 | Large Read fails with 5.00 although Block2 exists (Wakaama fixed 1024-byte encoders); 4.13 over 2048 bytes inbound | Wakaama | Keep Write payloads under 1024 bytes or use Block1 at SZX ≤ 1024. On 5.00 for an object-level Read, retry per instance | No |
| T47 | 4.01 causes an identical retransmission with the same MID | Wakaama | Dedup on MID per RFC 7252 §4.5 and replay the cached response | No |
| T48 | No ACK jitter (Wakaama); 4 s / 15 s ACK timeouts (NCS NB-IoT); separate responses (Leshan BS-Finish) | Wakaama, NCS, Leshan | Server-side ACK/response wait derived from per-client transmission parameters; NB-IoT profile with a long timeout. Support separate responses on every request | No |
| T49 | Bootstrap retransmissions ignored, not re-answered | Anjay Lite | BS server retries with a new MID and keeps every BS command idempotent | N/A |

### 3.3 Encoding (decoder tolerances; see vectors/)

| # | Quirk | Clients | Decoder handling | Forbidden? |
|---|---|---|---|---|
| T50 | TLV float as 4 bytes even for double resources | Wakaama | Accept 4- and 8-byte floats | No. TLV allows both |
| T51 | SenML without `bt`/`t`; `bn` only on the first record | Wakaama | Missing time means "now" (server receive time) | No (RFC 8428) |
| T52 | SenML `vlo` string label in CBOR | Wakaama, Zephyr | Accept `"vlo"` as the LwM2M objlnk extension | No. It's the LwM2M-defined label |
| T53 | Text objlnk with a corrupt separator byte | Wakaama [U] | Parse `^\d+.\d+$` leniently (any single non-digit separator) and log it | Deviation; accepting isn't forbidden |
| T54 | Text float `inf`/`nan` literal, ints with `.0` | Wakaama | Accept `.0` on Integer resources if the value is integral. `inf`/`nan` → reject the value and log it | No |
| T55 | LwM2M CBOR 11544 nested maps, also for a single resource | Anjay (1.2), Lite (default) | Full 11544 decoder, single and multi | N/A (spec format) |
| T56 | OMA-JSON emitted but not accepted (Anjay); SenML-ETCH accepted but never emitted | Anjay | Never send OMA-JSON Writes to Anjay; use ETCH only for Write-Composite/FETCH bodies | N/A |
| T57 | Legacy CF 1541-1543 | Leshan `-ocf`, Wakaama `_OLD_CONTENT_FORMAT`, Anjay legacy | Decode legacy codes as their modern equivalents. Never emit them | No |

### 3.4 Bootstrap

| # | Quirk | Clients | Server handling | Forbidden? |
|---|---|---|---|---|
| T58 | `GET /bspack?ep=` with Accept 112 before `/bs` | Anjay (1.2) | Serve Bootstrap-Pack. If disabled, answer 4.04 (Anjay falls back) | N/A |
| T59 | `/bs` without `pct` (1.0) or with `pct` (1.1+); Anjay retries without `pct` after a 4.xx | all | Accept with or without `pct`. Unknown `pct` → use TLV and ignore it, rather than reject | No |
| T60 | BS-Discover reports `lwm2m=1.0` for a 1.1 client (Leshan); `lwm2m="1.0"` with no `</>` (Anjay 1.0); unquoted (Wakaama) | Leshan, Anjay, Wakaama | The version from BS-Discover is advisory. Prefer `/bs` `pct` and the later DM Register | No |
| T61 | Separate response to Bootstrap-Finish; error forced CON with a text body | Leshan | Wait EXCHANGE_LIFETIME for a separate response | No |
| T62 | BS commands without CF treated as text (Wakaama); parse failure → 5.00 | Wakaama | Always set CF on BS Writes | N/A |
| T63 | BS-Read outside /1 and /2 → 4.00 | Wakaama, Anjay, Leshan | Read only /1 and /2 (README C6) | N/A |
| T64 | `DELETE /` deleting the Device object or all of /0 except the BS account | Wakaama | Re-write every object the session needs after `DELETE /` | No |
| T65 | Reconnect gap or modem offline after a Security write | NCS / nRF91 | Don't expect the DM Register within N seconds of Finish. Use no BS-session watchdog shorter than ~5 min | No |
| T66 | Server-initiated bootstrap (1.0 legacy): BS writes without a `/bs` request | Anjay (min 1.0), BC66 | Support BS-server-initiated sessions to registered 1.0 clients (Core 1.0 §5.2.x §?) | No |
| T67 | Non-default BS ports (T-Mobile 5584; AVSystem 5693/5694) | T-Mobile modems, Lite defaults | Bind BS on configurable ports. Never derive the BS URI from the DM URI | No |

### 3.5 Security

| # | Quirk | Clients | Server handling | Forbidden? |
|---|---|---|---|---|
| T68 | Restricted PSK suites: Lite **CCM_8 only**; Anjay without 0x00AE | Anjay Lite, Anjay | Offer at least TLS_PSK_WITH_AES_128_CCM_8 (0xC0A8), the RFC 7925 mandatory suite, plus 0xC0A4, 0x00A8, 0x00AE, and ECDHE-ECDSA-CCM_8 | No (RFC 7925 §4.2 §?) |
| T69 | PSK identity = endpoint name (NCS); `urn:imei-msisdn:` identity (Verizon); identity rewritten by the carrier library | NCS, Verizon | Never require identity == ep, but use it as a fallback when `ep` is missing (T6). Identity matching is exact-byte, up to 128 bytes | No |
| T70 | DTLS in modem firmware (nRF91) with modem-specific CID behaviour; NAT re-handshake every 60-90 s (u-blox, Quectel); `STOP_POLLING_AT_IDLE` keeps the CID session across address changes | NCS, u-blox, Quectel | RFC 9146 CID (server-assigned), session resumption (RFC 5077 or session-ID cache sized for the fleet), abbreviated handshakes | No |
| T71 | RPK basically absent on modems; X.509 chains rejected (Leshan client) | modems, Leshan | Provide PSK and X.509 single-cert paths for every BS profile | N/A |
| T72 | Leshan DTLS role BOTH: the server may start the handshake (non-queue) | Leshan | Optional. We never initiate DTLS as server (§?) | N/A |

---

## 4. Interop test matrix

### 4.1 Reference peers in CI

| Peer | Why | Run headless | Exercises |
|---|---|---|---|
| **Zephyr** `native_sim` | first fleet | per [zephyr-interop.md](zephyr-interop.md) (Twister/pytest) | 1.0/1.1, CON notifications, composite, Send, queue RX-off, CID, Update-after-rehandshake (C2) |
| **Anjay 3.15 demo** | the only open 1.2 client: LwM2M CBOR, Bootstrap-Pack, `edge/con/hqmax/epmin/epmax`, Discover `depth`, ETCH input, version step-down, TCP | `docker build -t anjay Anjay/` (repo Dockerfile, ubuntu:24.04 + mbedTLS) with `cmake -DWITH_UNSECURE_CONNECTIONS=ON . && make -j` [U: the flag must be added to the Dockerfile build], then `./output/bin/demo -e ep -u coap://srv:5683 -l 60 -t` (`-t/--disable-stdin` for headless). Version: `-v 1.0 -V 1.2`. Queue: `-q UQ`. PSK: `-s psk --identity-as-string ID --key-as-string KEY -u coaps://…`. Also `--use-connection-id`, `--ciphersuites`, `--confirmable-notifications`, `--retry-count/--retry-timer`, `-b` (bootstrap) (aj demo/demo_args.c:1152-1324; README.md:263-299) | 1.2 Register, `/bspack` → `/bs` fallback, 4.12 step-down 1.2→1.1→1.0, 11544 and SenML-CBOR defaults, ETCH Write-Composite, NON + CON notifications, attribute validation, TCP (`coap+tcp://`), re-Register on a new session, `/1/x/17-20` retry, CCM_8-only suites |
| **Anjay Lite test_app** | strict 1.2 client: drops malformed requests silently, 11544 everywhere, ETCH-CBOR, Location-Path limits | `cmake .. && make -j` at the repo root (mbedTLS 3.6.7 fetched). Drive `tests/integration/app` over JSON-RPC: `init {endpoint, servers:[{uri, security}]}` (al tests/integration/README.md:1-60). The tutorial binaries have a hard-coded URI (al examples/tutorial/BC-MandatoryObjects/src/main.c:39); patch or use test_app. Build a 1.1 variant with `-DANJ_WITH_LWM2M12=OFF` | 1.2/1.1 Register order (T1), `b=U` (T4), no-`</>` payload (T11), CON-only downlink (T33), empty-segment drop (T30), 4.15 Accept (T35), Location-Path limits (T21), BS retransmit (T49) |
| **Leshan client demo** | 1.1 hash-order queries, TLV default, `ep`-less Register, RPK, X.509, OSCORE (experimental), TCP/TLS via java-coap, separate BS-Finish | `wget https://download.eclipse.org/leshan/2.x/lastSuccessfulBuild/artifact/leshan-demo-client.jar` (ls README.md:73-74), or `mvn -pl leshan-demo-client -am install -DskipTests` (JDK 17). Run: `(sleep 5; echo update; sleep 2; echo "send current-value /3/0/9"; sleep infinity) \| java -jar leshan-demo-client.jar -u coap://srv:5683 -n ep1 -l 60` [U: stdin piping]. PSK `-i id -p hexkey`. RPK `-cpubk/-cprik/-spubk`. X.509 `-ccert/-scert`. CID `-cid on`. Queue `-q`. No `ep`: `-nm NEVER`. TCP: `-u coap+tcp://`. OSCORE: `-sid -msec -rid`. No Docker image: use `eclipse-temurin:17-jre` + the jar | T1, T5 (`b=UT`), T6 (no `ep`), T12 (quoted `ct` list), T18, T22, T34 (TLV), T37, T45, T61, T60; RPK/X.509/OSCORE handshakes; composite operations; Send in SenML-CBOR/JSON |
| **Wakaama lwm2mclient** | worst-case 1.0/1.1 client: no composite, NON-only notifications, 0-length-token Send, no re-Register on 4.04, missing CF on Update | `git submodule update --init` (tinydtls), then `cmake -S examples/client/udp -B b && cmake --build b` (or `examples/client/tinydtls` for PSK `-i/-s`). Run `sleep infinity \| ./b/lwm2mclient -4 -h srv -p 5683 -n ep -t 60` (keep stdin open; CI uses pexpect, wk tests/integration/conftest.py:85-99). 1.0 build: `-DWAKAAMA_CLIENT_LWM2M_V_1_0=ON` [U: exact cache variable name, see wk wakaama.cmake:14] | T3, T8, T15/16 (build variants), T17, T19, T25, T32, T41, T42, T43 (NON), T44, T46, T47, T50-T54, T62-T64; Block1 Register (`-S 16`) for T20 |

Run each peer in **NoSec**, **PSK**, and (Anjay, Leshan) **X.509**. For Anjay, also run **RPK**, **queue** and **bootstrap**. The ETS INT 1.2 mapping is in ets-1.2.md. Each row should carry ETS IDs once those are cross-referenced.

### 4.2 Not reproducible in CI (field-only)

| Peer | Coverage approach |
|---|---|
| nRF91 NCS sample (real modem) | Zephyr native_sim with NCS prj.conf values (lt 43200, queue, CID, `urn:imei:` ep, PSK id = ep) covers the logic. Modem DTLS/CID needs one HIL board in nightly runs |
| Nordic carrier library, Quectel, u-blox, HL78xx, Telit | No headless option. Replay recorded pcaps (when available) through the codec and RD tests. Use synthetic test vectors for T3, T5, T7, T24, T26, T28 |
| Operator DM servers | Not peers. Their constraints shape T7, T28, T67, T69 |

### 4.3 Coverage gaps (no open-source client peer)

These need self-test with our own Go test client, or Leshan *server* parity (leshan-tests.md):
- MQTT (M) and HTTP (H) bindings
- SMS and NIDD (Anjay commercial only)
- DTLS 1.3
- EST
- OSCORE (only Leshan, experimental)
- Gateway /25 (Anjay OSS, but default OFF: build with `-DWITH_LWM2M_GATEWAY=ON` [U: option name, see aj CMakeLists.txt:283] to cover it)
- Observe-Composite with attributes in the query (Anjay rejects it, T31; Lite disables Observe-Composite by default)

### 4.4 1.2 feature × peer

| 1.2 feature | Anjay | Anjay Lite | Leshan | Wakaama | Zephyr |
|---|---|---|---|---|---|
| `lwm2m=1.2` Register | ✓ | ✓ | – | – | – |
| LwM2M CBOR 11544 | ✓ | ✓ (default) | – | – | – |
| SenML-ETCH 320/322 (server→client) | ✓ | 322 | – | – | – |
| Bootstrap-Pack | ✓ | – | – | – | – |
| `edge` / `con` / `hqmax` | ✓ | ✓ | – | – | – |
| `epmin` / `epmax` | ✓ | ✓ [U] | ✓ (1.1) | – | – |
| Discover `depth` | ✓ | [U] | – | – | – |
| Gateway /25 | build flag | – | – | – | Zephyr opt-in (`LWM2M_GATEWAY_OBJ_SUPPORT`) |
| TCP binding | ✓ | – | experimental | – | – |
| DTLS CID | ✓ | ✓ | ✓ | – | ✓ |
