# LwM2M server (Go): specification

The goal is the most spec-compliant and most interoperable LwM2M Server and Bootstrap-Server available: **OMA LwM2M 1.2.2**, serving 1.0, 1.1 and 1.2 clients.
The first deployment is a Zephyr fleet, but the target is the spec, not the fleet.
This file is the entry point. It records the precedence rules, scope and resolved conflicts, maps every requirement to the tests that prove it, and sets the build order.

| File | What it is | Authority |
|---|---|---|
| [standards.md](standards.md) | OMA TS/ETS documents, RFC sections, ~110 server requirements (`GEN/REG/DM/ATT/OBS/SEND/QM/BS/SEC/FMT/DT/ID/VER/FW-nn`, 1.1.1-based with Δ1.2 flags), and the object registry | **Normative** |
| [standards-1.2.md](standards-1.2.md) | 273 requirement rows from 1.2.2 Core and Transport (+ Gateway TS 1.1.1), 1.1.1 SCR tables (1.2.x voided them), 30 ambiguities | **Normative (primary)** |
| [ets-1.2.md](ets-1.2.md) | All 155 OMA ETS INT 1.2 test IDs (with the 1.2.1-C deltas), configurations C.1–C.26, requirement mapping | **Primary acceptance suite** |
| [client-ecosystem.md](client-ecosystem.md) | Wakaama, Anjay, Leshan client, modem clients, operator requirements: quirks and a tolerance list | **Interop** |
| [zephyr-client-profile.md](zephyr-client-profile.md) | What the Zephyr client actually does on the wire | **Interop** (first fleet) |
| [zephyr-interop.md](zephyr-interop.md) | 66 Zephyr interop tests (58 ETS IDs, automated), plus the Leshan REST contract the harness depends on | **Automated acceptance** (first CI gate) |
| [leshan-tests.md](leshan-tests.md) | 389 Leshan test methods mined as an edge-case checklist, plus the implied server rules | **Reference behaviour** |
| [vectors/](vectors/README.md) | **803** golden codec vectors from Zephyr, Leshan, Wakaama, the TS's own examples and our own LwM2M CBOR vectors (`spec-examples.json`). Permissive sources only, see the licensing policy there | **Codec unit tests** |

Pinned sources: Leshan `22bc7531`, Zephyr `74b7173e`, OMA lwm2m-registry `7d5204dd`, OMA LwM2M TS **1.2.2 (primary)**, 1.1.1, 1.0.2, ETS INT 1.2 (2023-10-03), ETS INT 1.2.1-C (2024-03-12).

---

## 1. Precedence rule

When sources disagree, they rank in this order:

1. **OMA LwM2M TS 1.2.2 (Core + Transport) and the RFCs it references.** For a 1.0 or 1.1 client, the TS of that version governs that session (GEN-01).
2. **OMA ETS.** Its pass criteria interpret the TS where the TS is ambiguous.
3. **Interop tolerance (Postel, bounded).** Everything we **send** is strictly per spec. Known deviations from real clients (client-ecosystem.md, zephyr-client-profile.md) are **accepted** only when the spec does not forbid accepting them. Each tolerance is listed in §3 with the clients that need it. A tolerance never changes what we emit.
4. **Leshan behaviour.** Tie-breaker only, where the spec is silent.

**1.2.x has no conformance tables.** Appendix B of 1.2.0–1.2.2 is "voided" (standards-1.2.md §2). Where 1.1.1's tables said M/O, the 1.2.2 **Core text** now governs. Read literally, that text makes Read-, Write- and Observe-Composite mandatory for the Server, and that is the reading we adopt. Where 1.2.x weakened a requirement (security modes became "if supported", CID has no keyword, `<PROPERTIES>` became SHOULD), we still implement the stronger form. Being compliant under both readings is the goal.

The Leshan-compatible REST layer (package `leshanapi`) (§4) follows the interop harness's expectations. It is a test adapter and does not change core semantics.

---

## 2. Scope: all of LwM2M 1.2.2

**Versions:** clients declaring `lwm2m=1.0`, `1.1` or `1.2` (any 1.2.x) are served with the semantics of their declared version. Any other version gets 4.12 (REG-04).

**Interfaces:** Bootstrap (incl. Bootstrap-Pack-Request, Bootstrap-Discover/-Read, server-initiated bootstrap), Registration (incl. Profile ID), Device Management (all ops incl. Composite, Discover `depth`), Information Reporting (Observe/Composite, attributes in the Observe request, Send).

**Attributes:** pmin, pmax, gt, lt, st, epmin, epmax, edge, con, hqmax. Properties: dim, ssid, uri, ver, lwm2m.

**Content formats:** 0, 40, 42, 60, 110, 112, 320, 322, 11542, 11543, 11544. Legacy 1541–1543 are tolerated on input.

**Transports and bindings:**
| Binding | Stack | Phase |
|---|---|---|
| U (UDP) | CoAP / RFC 7252 + DTLS 1.2 (CID) / DTLS 1.3 | core |
| T (TCP) | CoAP over TCP / TLS 1.2, 1.3 (RFC 8323) | after core |
| S (SMS) | CoAP over SMS via a pluggable SMS gateway | late |
| N (Non-IP) | CoAP over NIDD via a pluggable gateway (e.g. SCEF/NEF), MTU 1358 (CIOT-01) | late (used in production: SoftBank, nRF carrier lib, Quectel) |
| LoRaWAN | `lorawan://{port}`, server acts as a LoRaWAN Application Server (LORA-01..03) | late |
| M (MQTT) | LwM2M over MQTT (1.2 Transport §7) | late |
| H (HTTP) | LwM2M over HTTP (1.2 Transport §8) | late |

**Security:** NoSec (with lower-layer security), **PSK, RPK, X.509** (all MUST), Certificate mode with EST, **OSCORE** (/21), DTLS 1.2 + **CID**, DTLS 1.3 / TLS 1.3, SNI, session resumption.

**Objects:** the full OMA registry including `version_history/`, plus vendor/third-party objects loaded at runtime. Server-side semantics are built in for /0, /1, /2 (ACL, when the server is Access Control Owner), /3, /5, /21, /23, /25, /26 (gateway).

**Other:** queue mode with a real downlink queue, hqmax-aware. Gateway (/25, /26) routing for end devices behind a gateway. Echo and Request-Tag (RFC 9175). Multi-server deployments (ssid scoping).

**Not in scope:** nothing outside the 1.2.2 TS. Everything above is planned, and phasing is in §6.

---

## 3. Conflicts resolved

Each row is either **spec** (the spec decides, and the row records the reading) or **tolerance** (a client deviation we accept on input; it never changes what we emit). The full cross-client tolerance list is in client-ecosystem.md §3. Rows from there get merged here as T-numbered entries.

| # | Conflict | Decision |
|---|---|---|
| C1 | **Register ep/identity mismatch code**: spec says 4.00 (REG-06, SEC-06), Leshan uses 4.03. | **4.00** per spec. 4.03 only for "ep not allowed" (policy). Zephyr only branches on success. |
| C2 *(tolerance)* | **New DTLS session or new address.** Spec: the client re-registers (REG-17). Real clients differ: Zephyr sends an **Update** after re-handshaking; Anjay re-registers; Quectel sends nothing after an IP change. | Look up registrations by **CID, then (DTLS session, authenticated identity), never by address alone.** Accept an Update when the authenticated identity equals the registration's identity, and rebind the address. Otherwise reply 4.04. Note that Wakaama then gives up instead of re-registering, so the native API flags clients that go silent after a 4.04. |
| C3 | **Queue mode**: the spec says the server holds downlink (QM-02); Leshan's REST returns `{"delayed":true}`, which breaks the interop harness. | The core queues per spec. The **`leshanapi` REST** blocks until delivered or the request timeout, and never returns `delayed`. The **native API** exposes async queued commands. |
| C4 | **Address change without DTLS** (NoSec): Leshan drops notifications and rejects Send from the new IP:port. | Same, since NoSec is dev-only. Exception: int-105, an unsecured De-register from any peer, is accepted when the endpoint has no security info. |
| C5 | **Active cancel** (`?active`): Leshan keeps the stored observation after a successful CoAP cancel. | Remove it locally too. The interop tests don't depend on the Leshan behaviour. |
| C6 | **Bootstrap-Read target** /1 or /2 (C vs T disagree, BS-06). | The BS server issues Bootstrap-Read only on /2 (valid under both readings); it accepts responses for either. Check the 1.2.2 text in standards-1.2.md. |
| C7 *(tolerance)* | **Zephyr encoder quirks vs the spec** (see vectors/README): `vlo` text key, `bn="/o/i/"` + `n="r"`, objlnk with a trailing NUL in CBOR, CBOR time tag 0 + RFC 3339, OMA JSON labelled ct 50, `</>;ct=…` with no `rt`. | **Decoders tolerate all of these. Encoders emit the spec form.** The vectors cover both. |
| C8 *(tolerance)* | **Write replace vs partial**: Zephyr treats PUT the same as POST (partial). | Server sends the correct method. Don't rely on replace semantics clearing resources on Zephyr. |
| C9 *(tolerance)* | **Create response**: Zephyr returns no Location-Path. | Always send the instance id in the Create payload. Never require Location. |
| C10 *(tolerance)* | **Read-Composite request body**: Zephyr parses it with the Accept format. | Always set Accept equal to Content-Format on FETCH. |
| C12 | **Emit rules derived from the client survey** (all of them spec-legal) | **Always** send Accept (client defaults differ: TLV, text, SenML-CBOR, LwM2M CBOR). **Always** CON for downlink (Anjay Lite drops NON). Composite FETCH/iPATCH have **no Uri-Path and no Uri-Query**, and attributes go via Write-Attributes. Location-Path is `/rd/<id>` with id ≤ 32 B (Anjay Lite allows ≤ 2 segments of ≤ 40 B). **Never** send Location-Query. Unsupported version gets **4.12** (Anjay falls back only on 4.12). Paths have no empty or trailing segments. |
| T1–T74 | Per-client input tolerances | See client-ecosystem.md §3. Only T15 (/21 in the Register list) is something the spec forbids the client to send, and ignoring it on input is allowed. |
| C11 | **Device object version**: a 1.1 Zephyr reports `</3>;ver=1.0`. | Version is resolved per object from `ver`, falling back to the default for the client's `lwm2m` version (VER-01). |

---

## 4. Northbound API

Two surfaces, one core.

1. **Leshan-compat REST**, enough to run `zephyr-interop.md` unchanged. Contract in zephyr-interop §3–§4. Checklist in zephyr-interop §6. Notable rules:
   - Device outcomes are returned as HTTP 200 with `status: "NAME(code)"`.
   - Node JSON uses int ids, INTEGER as strings and real JSON booleans.
   - Absent composite paths are **omitted**, never `null`.
   - The SSE `/api/event` stream heartbeats every ≤5 s and tolerates the malformed `?<ep>` query.
   - Bootstrap config and security store REST live on :8081.
2. **Native API**: designed later, not constrained by Leshan. It covers commands (sync, or async/queued), an event stream (registration, update, deregistration, notification, send, command result), and a security/device registry.

The `leshanapi` layer is a thin adapter over the native core. It is a test fixture first; offering it to users is optional.

---

## 5. Requirement → test matrix

Columns:
- **Phase**: the build milestone from §6.
- **Interop**: Zephyr tests (int-NNN = `test_LightweightM2M_1_1_int_NNN`; bw-N = `test_blockwise_N`; att-* = `test_observe_attributes`).
- **Leshan area**: the section in leshan-tests.md to mine for extra edge cases.
- **Own test**: tests we must write because nothing covers the requirement.

Requirement IDs are defined in standards.md §3.

### 5.1 General / CoAP
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| GEN-01 1.0+1.1 clients | M2 | all (1.1) | registration | 1.0 Register (`b=UQ` style) |
| GEN-02 ignore unsupported | M2 | int-236 | composite ops | unknown resource in Read reply |
| GEN-03 CON except Notify/Execute | M0 | – | – | message layer |
| GEN-04 CoAP/Observe/Block | M0 | bw-1..4 | – | dedup, retransmit, MID/token tables |
| GEN-05 UDP | M0 | all | multiple transports | – |
| GEN-06 response code map | M3 | int-221, int-256 | read/write | every T §6.7 code maps |
| GEN-08 alternate path `rt="oma.lwm2m"` | M3 | – (Zephyr never sends) | registration | prefix applied |
| GEN-09 validate paths | M3 | – | read | malformed path refused locally |
| GEN-10 no DM before Register ACK | M2 | – | registration | ordering |

### 5.2 Registration
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| REG-01..03 params | M2 | int-101, int-102 | registration | missing `lt`/`lwm2m` gives 4.00 |
| REG-04 version 4.12 | M2 | – | registration | lwm2m=1.2 / 9.9 |
| REG-05 Location `/rd/<id≤32B>` | M2 | int-105 (uses id) | registration | id length |
| REG-06 error codes (C1) | M2 | – | registration | each code |
| REG-07..10 link payload, ignore unknowns, `ct=` | M2 | int-101 | registration | `vectors/link_format.json` |
| REG-11 address bind (+CID) | M1/M2 | all | multiple transports | NAT rebind with CID |
| REG-12 re-register replaces, drops observations | M2 | int-241, int-103 | registration | observations gone |
| REG-13 lifetime expiry | M2 | int-107 | lifetime expiry | **end-to-end expiry (Leshan gap)** |
| REG-14..15 Update semantics, list replaces | M2 | int-102, int-104, int-227 | update | Update with changed object list |
| REG-16 De-register | M2 | int-103, int-105 | deregister | – |
| REG-17 new session (C2) | M2 | – | PSK, timeouts | re-handshake + Update accepted |
| REG-18 binding parsing 1.0 & 1.1 | M2 | int-108 | queue mode | `b=UQ` vs `b=U&Q`; **invalid binding (Leshan gap)** |
| REG-19 update trigger /1/x/8 | M3 | int-104 | execute | – |
| REG-22 store `sms` | M2 | – | – | parse only |

### 5.3 Device management
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| DM-01/02 Read | M3 | int-201..204, int-222..225, int-237 | read | Accept unsupported gives 4.06 |
| DM-03 ignore unknown resources | M3 | int-236 | read | – |
| DM-04 Discover | M3 | int-260, int-261, int-1630, int-1635 | discover | `dim`, inherited attrs |
| DM-05/06 Write replace/partial (C8) | M3 | int-205, int-212, int-215, int-220, int-226..228, int-233/234, int-256 | write | type validation before send |
| DM-07 Write-Attributes | M4 | int-260, int-261, att-wrong_res_type | write-attributes | valueless key unsets |
| DM-08 Execute | M3 | int-103, int-104, int-241, int-4, int-5 | execute | args ABNF |
| DM-09 Create (C9) | M3 | int-228, int-308, int-1630 | create/delete | – |
| DM-10 Delete | M3 | int-309, int-1635 | create/delete | – |
| DM-11 /0 gives 4.01 | M3 | int-221 | read/write | – |
| DM-12 composite read/write (C10) | M5 | int-229, int-230, int-235, int-236, int-257, int-280, int-281 | composite ops | – |
| DM-13/14 object-level errors, 4.01 surfaced | M3 | int-221 | – | – |

### 5.4 Attributes, Observe, Send
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| ATT-01..06 pmin/pmax/gt/lt/st + validation | M4 | int-301, att-step, att-greater_than, att-less_than (actually `gt`) | write-attributes | `lt` crossing (interop bug: never tested), lt+2st<gt |
| ATT-07 epmin/epmax | M4 | int-261 (written, ignored by Zephyr) | write-attributes | pass-through only |
| OBS-01 Observe | M4 | int-301 | observe/cancel | token reuse |
| OBS-02 cancel RST / Observe=1 | M4 | int-302, int-303 | observe/cancel | RST on unknown token |
| OBS-03 re-observe after every Register | M4 | – | observe/cancel | core keeps "desired observations" and re-establishes them |
| OBS-04 Notify CON/NON, ordering (7641 §3.4) | M4 | int-301 | observe/cancel | reordering, CON ACK ≤ ACK_TIMEOUT |
| OBS-05 composite observe/cancel exact list | M5 | int-304, int-305, int-308, int-309, int-310 | observe/cancel, composite | extra path rejected |
| SEND-01/02 `/dp` | M5 | int-306, int-307, int-311, bw-4 | send | unregistered object gives 4.04 |

### 5.5 Queue mode
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| QM-01 support | M6 | int-108, int-109 | queue mode | – |
| QM-02 hold + drain one at a time (C3) | M6 | – | queue mode | **queued command delivered on wake (Leshan doesn't queue)** |
| QM-03 awake window 93 s | M6 | int-109 | queue mode | timer reset on any uplink |
| QM-04 report failures | M6 | – | timeouts | – |
| QM-05 wake with new IP: CID or re-handshake | M1/M6 | – | – | **real cellular device** |
| QM-06 NAT | M1 | – | – | – |

### 5.6 Bootstrap
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| BS-01 Bootstrap-Request | M2b | int-0, int-1 | bootstrap | unknown ep gives 4.00 |
| BS-03/04 Write/Delete, order Delete→Write→Finish | M2b | int-1, int-4 | bootstrap | – |
| BS-05 Discover | later | – | bootstrap | – |
| BS-07/08 Finish, < 247 s | M2b | int-1, int-6, int-7 | bootstrap | session timeout |
| BS-10 PSK bootstrap | M2b | int-1 (port 5784) | bootstrap, PSK | – |
| BS-11 server-initiated (/1/x/9) | M3 | int-5, int-4 | bootstrap | – |

### 5.7 Security
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| SEC-01/02 DTLS server, mutual auth | M1 | all PSK tests | PSK | – |
| SEC-04 PSK suites | M1 | all PSK tests (CCM_8) | PSK | CBC_SHA256 handshake |
| SEC-05 identity ≤128 B, key ≤64 B | M1 | – | PSK | long identity/key (Zephyr default key buffer 16 B) |
| SEC-06 ep ↔ identity binding (C1) | M2 | int-1 (`secure`) | PSK, X509 | mismatch gives 4.00 |
| SEC-07 NoSec rules | M2 | int-101 (nosec), int-105 | – | NoSec refused when PSK exists for ep |
| SEC-11 session resumption | M1 | – | PSK | abbreviated handshake |
| SEC-12 alert handling, drop bad MAC | M1 | – | PSK | – |
| SEC-13 CID (RFC 9146) | M1 | all PSK tests (CID on) | – | **server-assigned CID; address change with CID; check Zephyr mbedTLS uses RFC 9146, not the draft** |
| — security removal closes sessions | M2 | – | PSK | (leshan-tests "non-obvious") |

### 5.8 Formats, types, IDs, versions
| Req | Phase | Interop | Vectors | Own test needed |
|---|---|---|---|---|
| FMT-01/02 all 1.1 formats | M0c | int-201, 203, 204, 211, 231, 232 | tlv, plain_text, cbor, senml_cbor, senml_json, oma_json, link_format | **opaque, objlnk parsing (no vectors exist)** |
| FMT-03 Content-Format/Accept | M3 | int-237 | – | – |
| FMT-04 TLV wrapping rules | M0c | int-203, int-215 | tlv | – |
| FMT-05 SenML resolution (C7) | M0c | int-231..234 | senml_* | Zephyr `bn`/`n` split |
| FMT-06 legacy 1541–1543 | M0c | – | – | tolerate on input |
| DT-01/02 types | M0c | int-257 (time) | all | float32 vs float64, time tag 0/1 |
| ID-01/02 16-bit IDs, unique ep | M0c/M2 | – | path | MAX_ID rejection |
| VER-01/02 object versions (C11) | M2 | int-101 | link_format | `version_history/` loaded |

### 5.9 Firmware update
| Req | Phase | Interop | Leshan area | Own test needed |
|---|---|---|---|---|
| FW-01/02 push Block1 /5/0/0 | M7 | bw-1, bw-2 (abort + restart) | – (Leshan gap: no block-wise tests) | Block1 abort on timeout |
| FW-02 pull /5/0/1 (we serve Block2, same token every block, no query in URI) | M7 | – | – | **pull from our CoAP file server** |
| FW-03/04 state machine, result | M7 | bw-1 | – | state/result polling |
| FW-05 reconcile after update | M7 | int-241 | – | object list changed on re-register |
| Block2 downlink reads 4 KiB | M7 | bw-3 | – | – |

---

### 5.10 1.2-specific groups (from standards-1.2.md)
Requirement rows are in standards-1.2.md. ETS IDs are from ets-1.2.md, and "own" means no open-source client can exercise the case, so our Go test client (§6, track T) drives it.
| Group | Rows | Phase | ETS | Peer that can test it | Vectors |
|---|---|---|---|---|---|
| CBOR (11544) | 12 | M0c, M3 | 2xx in LwM2M CBOR | Anjay, Anjay Lite | spec-examples (9); **own vectors needed** |
| ETCH (320/322) | 7 | M0c, M5 | composite cases | Anjay, Anjay Lite | **own vectors needed** |
| PROF (Profile ID) | 11 | M2 | int-110, int-111 | **own** | none (spec has no hash examples) |
| BS (Bootstrap-Pack) | 30 | M2b | int-11, int-12 | Anjay, Anjay Lite | **own vectors needed** |
| DM (Discover depth) | 20 | M3 | int-264, int-266 | Anjay, Anjay Lite | **own vectors needed** |
| ATT/OBS (edge, con, hqmax, attrs in Observe) | 12 / 10 | M4 | int-312, int-313 | Anjay | **own vectors needed** |
| CID | 3 | M1 | – | Zephyr, Anjay | – |
| TLS13 | 6 | M8 | – | **own** | – |
| OSC (OSCORE) | 9 | M8 | – | Leshan (1.1), **own** for 1.2 | – |
| EST | 3 | M8 | int-10 | **own** | – |
| GW (/25, /26, Gateway TS 1.1.1) | 9 | M9 | – | Anjay (gateway build option) | **own vectors needed** |
| TCP | 4 | B1 | – | Anjay, Leshan | – |
| MQTT | 13 | B2 | int-19 | **own** | spec-examples (MQTT, own encoding) |
| HTTP | 10 | B3 | – | **own** | – |
| SMS / CIOT / LORA | 7 / 1 / 3 | B4–B5 | – | **own** | spec-examples (SMS trigger) |

## 6. Build order (milestones)

Each milestone ends with its tests green. Don't start the next one until they pass.
**Phase A** delivers a complete LwM2M 1.2.2 server over CoAP/UDP with all security modes. **Phase B** adds the other bindings.

### Track T (parallel, starts with M0): Go LwM2M test client
A scriptable client built on our own CoAP and codec packages. It drives every ETS case no open-source client can (Profile ID, MQTT, HTTP, EST, OSCORE 1.2, DTLS 1.3), serves as the raw CoAP injector the harness needs, and keeps CI from depending on Anjay's non-commercial licence. Reference peers in CI are Anjay (Docker), Anjay Lite, the Leshan client, Wakaama and the Zephyr interop suite (client-ecosystem.md §4). ETS fixtures needed: our own CA, DNS for `server.example.com` / `server-fail.example.com`, a firmware download server, and a second server instance.

### Phase A: complete 1.2.2 over CoAP/UDP
| M | Deliverable | Exit criteria |
|---|---|---|
| **M0** | CoAP message layer, transport-agnostic (UDP first): both roles per peer, MID dedup, CON retransmit, token-matched pending requests, Block1/Block2 with Request-Tag, Echo. Options: `plgd-dev/go-coap` if its server-accepted conn can issue requests; otherwise build on its codec. | Unit tests; RFC 7252 §4.8, 7959, 9175 |
| **M0c** | Codecs: **all** content formats incl. **LwM2M CBOR (11544) and SenML-ETCH (320/322)**, plus link-format, paths, attributes (incl. edge/con/hqmax). Object model loader (registry plus version_history plus runtime objects). | **All vectors** incl. `spec-examples.json` pass |
| **M1** | DTLS 1.2 via pion/dtls: **PSK, RPK, X.509** suites per SEC-04/09/10, **server-assigned CID**, identity lookup, SNI, resumption, session store interface. Real Zephyr device on cellular in week 1 for CID plus NAT rebind. | Real-device handshake; rebind survives; RPK/X.509 handshakes vs reference clients |
| **M2** | Registration incl. **Profile ID**, all binding syntaxes 1.0–1.2, expiry, ep↔identity binding (all modes), NoSec rules, registry store. | REG-*, PROF-*, SEC-06/07; int-101, int-105 |
| **M2b** | Bootstrap server: Request, Write, Delete, Discover, Read, Finish, **Bootstrap-Pack-Request**, server-initiated, all security modes, `leshanapi` bootstrap REST. | BS-*; int-0, 1, 4, 5, 6, 7 |
| **M3** | All DM ops incl. Discover `depth` + `leshanapi` REST + response mapping. | DM-*; int-102..104, 107, 201–228, 237, 241, 256, 1630, 1635 |
| **M4** | Observe + all attributes (incl. edge/con/hqmax, **attributes in the Observe request**) + events. | ATT-*, OBS-*; int-260, 261, 301–303, 312/313, att-* |
| **M5** | Composite ops (SenML and ETCH, LwM2M CBOR) + Send. | DM-12, OBS-05, SEND-*; int-229..236, 257, 280, 281, 304–311 |
| **M6** | Queue mode with real queue + hqmax-aware delivery + native async API. | QM-*; int-108, 109 |
| **M7** | FOTA push/pull (/5 v1.0–1.2 state machines), CoAP Block2 file serving. | FW-*; bw-1..4 |
| **M8** | OSCORE (/21), EST certificate mode, DTLS 1.3, ACL (/2) as Access Control Owner, multi-server ssid scoping. | OSC-*, EST-*, TLS13-* |
| **M9** | Gateway objects /25, /26: routing and addressing of end devices behind a gateway. | GW-* |
| **M10** | Scale-out: external session/registration store, CID-based routing, restarts without forcing re-handshakes. | Kill a node mid-fleet, devices keep sessions |

**Gates:** every ETS INT 1.2 case runnable with an open-source reference client passes (ets-1.2.md §4). All 66 Zephyr interop tests are green from M7 on.

### Phase B: other bindings
| M | Deliverable |
|---|---|
| **B1** | CoAP over TCP / TLS (RFC 8323), binding T |
| **B2** | MQTT binding (1.2 Transport), binding M |
| **B3** | HTTP binding (1.2 Transport), binding H |
| **B4** | Non-IP / NIDD (binding N) and SMS (binding S) via pluggable gateways |
| **B5** | LoRaWAN (server acts as Application Server) |

---

## 7. Fleet / firmware notes (not server code, but they decide behaviour)

- **Zephyr's default lifetime is 30 s**, which means an Update every 20 s. Set /1/0/1 through bootstrap (the interop suite uses 86400).
- **Queue mode without session caching**: every wake-up is a full handshake plus Register, which wipes observations. Enable `CONFIG_LWM2M_TLS_SESSION_CACHING`, or use the `*_AT_IDLE` modes that keep the socket.
- Zephyr's outbound block-wise is off by default (max message ~1195 B with DTLS+CID). Large device responses fail with 5.00 unless enabled.
- PSK key buffer defaults to 16 B. Use 16-byte keys or raise the Kconfig.
- Zephyr defaults to LwM2M **1.0**. Composite ops, Send and SenML need **1.1** (`CONFIG_LWM2M_VERSION_1_1`).

## 8. Open questions

1. Device hardware: **nRF91** (modem-offloaded DTLS) or Zephyr + mbedTLS? This decides how CID and resumption actually behave.
2. Firmware LwM2M version: 1.0 or 1.1? (Zephyr can't do 1.2, so 1.2 interop testing needs Anjay/Wakaama/Leshan clients.)
3. Is queue mode enabled on the fleet, and with which idle mode?
4. FOTA: push or pull today? Is there an existing image server?
5. Scale target (devices per node, total) for M8 sizing.

## 9. Known gaps in this spec
- **We must write our own 1.2 vectors** (LwM2M CBOR beyond the TS examples, SenML-ETCH, opaque, 1.2 attributes, Discover depth, Bootstrap-Pack, Profile ID), cross-checked live against Anjay / Anjay Lite in CI. AVSystem-derived vectors were deliberately excluded (non-commercial licence).
- **The spec has errata** (standards-1.2.md §4, ets-1.2.md inline). 4 of the 6 TLV examples in the TS are inconsistent and are vectorised as must-fail. The MQTT Send example uses the wrong opcode. The HTTP Bootstrap-Read table is stale. About 25 ETS errors are flagged.
- **Profile ID dynamic mode defines no canonical hash input**, so the server can only cache IDs it learned from a full registration (PROF rows).
- The conditional-attributes draft that 1.2 notification rules defer to was not fetched.
- CoAP retransmit defaults on the Zephyr side are upstream values, not verified in the sparse clone.
- The Leshan Docker launch flags and SSE heartbeat for the Zephyr harness weren't in the clone.
- Several modem and operator documents were unreachable (client-ecosystem.md, [NA] marks). CI peer commands there are untested.
- leshan-tests.md and client-ecosystem.md rows were spot-checked, not all re-verified against the source.
