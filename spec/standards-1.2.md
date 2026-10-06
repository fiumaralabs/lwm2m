# LwM2M 1.2.2 Server / Bootstrap-Server Compliance Checklist

Companion to `standards.md`, which stays the inventory. That file's §3 is based on 1.1.1. This file is the full 1.2.2 checklist. Compiled 2026-10-06. Every row paraphrases text extracted with `pdftotext` (and cross-checked against the HTML rendering) from these documents, all fetched from `https://www.openmobilealliance.org/release/` on 2026-10-06:

| Short | Document | Fetched from | Used for |
|---|---|---|---|
| **C** | OMA-TS-LightweightM2M_Core-V1_2_2-20240613-A (156 p.) | `LightweightM2M/V1_2_2-20240613-A/` (PDF + `HTML-Version/`) | primary |
| **T** | OMA-TS-LightweightM2M_Transport-V1_2_2-20240613-A (110 p.) | same | primary |
| **ERELD** | OMA-ERELD-LightweightM2M-V1_2_2-20240613-A | same | change list (§3.5) |
| **GW** | OMA-TS-LWM2M_Gateway-V1_1_1-20240312-A (20 p.) | `LwM2M_Gateway/V1_1_1-20240312-A/` | GW group only. The Core TS defines objects /25 and /26 but no gateway procedures. Those live in this separate enabler. |
| C121, T121 | 1.2.1 Core/Transport (20221209) | `LightweightM2M/V1_2_1-20221209-A/` | text diff, used to tag rows "1.2.2" |
| C120, T120 | 1.2 Core/Transport (20201110) | `LightweightM2M/V1_2-20201110-A/` | SCR check, Bootstrap-Read history |
| C111, T111 | 1.1.1 Core/Transport (20190617) | `LightweightM2M/V1_1_1-20190617-A/` | §2 SCR tables (last published ones) |
| RFC 8790, RFC 6920 | rfc-editor.org text | | ETCH rules, Profile-ID suite IDs |

Scratch copies of the files and extracted text are in `scratchpad/specs/`. Nothing below comes from memory. Where a row goes beyond the text, it is labelled "derived" or "recommended reading".

**ERELD 1.2.2 (§3.5) change list.** It is editorial or bug-fix only: RFC 2119 replaced by RFC 8174, broken appendix links, core object versioning clarified, object 27 range fixed, Transport App. C diagrams, "clarified UDP bindings in section 6.2", "clarified 6.4.4 error codes for CoAP", MQTT ETS updated. It links to a "deltas" page that is not part of the release. The 1.2.1→1.2.2 text diff (done here) found these normative changes:

- Profile-ID fingerprints must include object versions.
- "UDP SHALL be supported by the Server; TCP optional" (C §6.2.1.2).
- Discover returns *attached or inherited* attributes.
- The Discover sentence "optional resources … MUST NOT be interpreted as an error by the Server" was removed.
- /5 v1.2 resource 14.
- LoRaWAN "Application Server MUST reply".
- BCP 14 now means upper-case keywords only.

## 0. Legend

Columns: **ID | Requirement | § | Level | New-in | Maps-to**.

- **§**: C/T/GW section. "Tbl" = table.
- **Level**: the BCP 14 keyword as written. MUST also covers MUST NOT, SHALL and REQUIRED; SHOULD covers RECOMMENDED; MAY covers OPTIONAL. Two extra values:
  - **desc**: a lowercase or descriptive statement that still binds behaviour (e.g. "the Server removes the registration").
  - **(c)** after a level: a client requirement that the server must expect or tolerate.
- **New-in**: the first version where the requirement appears in this form.
  - 1.2.1 and 1.2.2 come from the 1.2.1 change lists (C §1.2, T §1.2) and the 1.2.1↔1.2.2 diff above.
  - 1.0 vs 1.1 follows `standards.md` §3 (C10/1.1.1).
  - "1.2" means present in 1.2.0.
- **Maps-to**: an existing `standards.md` ID. **=X** means same requirement. **X Δ** means X with a 1.2.x change (the row says what). **NEW** means not in `standards.md`. An existing ID is reused as the row ID when it matches. New IDs continue each group's numbering.
- New groups: PROF, CBOR, ETCH, GW, MQTT, HTTP, TCP, SMS, OSC, EST, TLS13, CID, plus three the text needed: OBJ (object-level server duties), LORA (LoRaWAN binding), CIOT (3GPP CIoT appendix).

---

## 1. Requirements

### 1.1 GEN: compatibility, general CoAP mapping

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| GEN-01 | Server MUST support clients of its own major.minor version (1.2 ↔ 1.2). Server SHOULD support all client versions of the same major (1.0, 1.1). Forward compatibility (a newer client on an older server) is "also recommended" (lowercase). | C §5.1 | MUST/SHOULD | 1.1 | =GEN-01 |
| GEN-02 | Operations on optional objects, object versions or resources that the peer does not support MUST NOT be interpreted as errors, when possible. | C §5.2, §5.3 | MUST | 1.1 | =GEN-02 |
| GEN-03 | Every CoAP-layer LwM2M operation MUST be Confirmable, except Notify, Execute and (since 1.2.1) Send, which MAY be NON. Stated in the UDP binding section. | T §6.8.1 | MUST | 1.0 (Send 1.2.1) | GEN-03 Δ |
| GEN-04 | Client and server MUST support CoAP (RFC 7252). Both MUST support Observe (RFC 7641) for Information Reporting. Server MUST support block-wise (RFC 7959). Client MUST support block-wise if it implements /5. Block-wise for other objects is client-optional, and "how a server uses it is out of scope". | T §6.1 | MUST | 1.0 | =GEN-04 |
| GEN-05 | Server MUST support the UDP binding (T §6.8). "UDP SHALL be supported by the Server for binding and support for TCP is Optional". Clients SHALL assume U even if /1/x/7 omits it. The 1.1 "SHOULD support SMS" for servers is gone. | T §6.8; C §6.2.1.2 | MUST | 1.0 (wording 1.2.2) | GEN-05 Δ |
| GEN-06 | The listed response codes (T §6.7, §7.3, §8.5) are the only valid ones. When no 4.xx fits, the client MUST return a 5.xx (HTTP 5xx; MQTT 500/501/503). | T §6.7, §7.3, §8.5 | MUST (c) | 1.1 | =GEN-06 |
| GEN-07 | Client and Server MUST be able to verify freshness of certain operations. Echo SHOULD be implemented and used. Request-Tag SHOULD be implemented by block-wise implementations, to stop block interchange when the replay window is >1. Neither attack applies over TCP or with a DTLS replay window of 1. | T §5.5, §5.6, §6.1 | MUST/SHOULD | 1.1 | GEN-07 Δ |
| GEN-08 | Alternate path. The client MAY add a link `rt="oma.lwm2m"`, which MUST NOT contain a numeric segment. For DM and IR the Server MUST prepend it (`GET /lwm2m/3/0/0`). SenML JSON/CBOR and SenML-ETCH names MUST include it (1.2.1). The Bootstrap-Server MUST use plain `/o/i/r`, and bootstrap SenML names MUST NOT include it. | T §6.4.1, §7.1.1 | MUST | 1.1 (SenML 1.2.1) | GEN-08 Δ |
| GEN-09 | A path that breaks the Core rules (e.g. a resource-instance ID without a resource ID) MUST be answered 4.05 by the client. The Server should validate paths before sending. | T §6.4.4 | MUST (c) | 1.1 (wording 1.2.2) | =GEN-09 |
| GEN-10 | Server SHOULD NOT send DM/SE or IR operations before it has replied to the Register. The client MUST ignore them until registration completes. | C §6.3, §6.4 | SHOULD | 1.0 | =GEN-10 |
| GEN-11 | BCP 14 keywords are normative only in capitals (RFC 8174). Lowercase "must" in examples, notes and some tables is not normative. All sections except Scope and Introduction are normative unless marked informative. | C §3.1; T §3.1 | — | 1.2.2 | NEW |
| GEN-12 | Current Transport Binding U/M/H/T/S/N: the Server MUST send requests over that binding, and the client MUST respond over it. Only one binding is used for the whole Transport Session (desc). | C §6.2.1.2 Tbl 6.2.1.2-1 | MUST | 1.1 (M,H 1.2) | NEW |
| GEN-13 | A change to binding-related configuration SHALL NOT affect the current Transport Session. It SHALL apply to future sessions only, and SHALL NOT affect the existing registration. | C §6.2.1.2 | MUST | 1.1 | NEW |
| GEN-14 | If /1/x/22 Preferred Transport is defined, the client SHALL use it to initiate the connection. The resource holds a single binding (SHALL). Otherwise the client picks a binding common to /1/x/7 and /3/0/16. The Server can write /1/x/22 to force a transport (e.g. U for FOTA). | C §6.2.1.2; E.2 res 22 | MUST (c) | 1.1 | NEW |
| GEN-15 | Execute /1/x/8 may carry a binding-override argument. If the client supports it and the binding is listed in /1/x/7, the client SHALL use it and SHALL immediately reconnect over it when it differs from the current one. The Server MUST only offer bindings listed in /1/x/7 (derived). | C §6.2.1.2; T §6.6.1 | MUST (c) | 1.1 | REG-19 Δ |

### 1.2 REG: client registration

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| REG-01 | Register = CoAP/HTTP POST `/rd?ep={ep}&lt={s}&lwm2m={v}&b={b}&Q&sms={msisdn}&pid={pid}` with the object list as payload. The Server MUST support every parameter and payload in Tbl 6.2.1-1 except Profile ID. The Server MUST support all operations of this interface. Query names: ep, lt, lwm2m, b, Q, sms, pid. | C §6.2, §6.2.1; T §6.4.3 Tbl 6.4.3-1/-2 | MUST | 1.0 (pid 1.2) | REG-01 Δ |
| REG-02 | `ep` is optional, but MUST be sent when the security protocol gives no authenticated identifier or the server cannot access it. The client SHOULD send it. When sent it MUST be unique on that server. Without it, the server identifies the client from the security identity. | C §6.2.1, Tbl 7.4-1, §7.4.1 | MUST/SHOULD | 1.0 req / 1.1 opt | =REG-02 |
| REG-03 | `lt` and `lwm2m` are required. `lt` MUST equal /1/x/1, and a write to /1/x/1 by either side becomes the new lifetime. `lwm2m` MUST be an approved version number. | C §6.2, Tbl 6.2.1-1 | MUST | 1.0 | =REG-03 |
| REG-04 | Server MUST refuse a Register whose `lwm2m` version it does not support (4.12 Precondition Failed). | C §6.2.1; T Tbl 6.7-2 | MUST | 1.1 | =REG-04 |
| REG-05 | Success = 2.01 Created with Location-Path. The Server MUST return a location under `/rd` (e.g. `/rd/5a3f`). HTTP: the response includes the path, under /rd. MQTT has no location; the ENDPOINT topic level plays that role (derived). | T §6.4.3, §7.1.3 | MUST | 1.0 | =REG-05 |
| REG-06 | Register response codes: 2.01 · 4.00 (missing mandatory or unknown param, unknown ep, ep ≠ cert CN) · 4.01 (not authorized; with OSCORE, rejection of an initial request, 1.2.1) · 4.03 (ep not allowed) · 4.09 (registration cannot be used to find the object list) · 4.12 (version). | T Tbl 6.7-2 | MUST | 1.1 (4.01 1.2.1) | REG-06 Δ |
| REG-07 | Payload media type, when present, MUST be application/link-format (40). Each object is a link whose target is the object or instance path, with `ver` when §7.2 requires it. Any other link parameter MUST NOT be interpreted as an error unless the enabler defines it. | C §6.2.1 | MUST | 1.0 | =REG-07 |
| REG-08 | An object defined outside the enabler, unknown to or unsupported by the server, MUST NOT be interpreted as an error and does not block Register or Update. | C §6.2.1, §6.2.2 | MUST | 1.0 | =REG-08 |
| REG-09 | The object list MUST NOT contain /0, /21 or (1.2.1) /23. /24 MQTT Server is not excluded (see A-17). The client MUST implement LwM2M Version, Lifetime and the object list. | C §6.2.1 Tbl 6.2.1-1 | MUST (c) | 1.0 (/21 1.1, /23 1.2.1) | REG-09 Δ |
| REG-10 | The client MAY advertise optional data formats on the root link: `</>;ct=110` or quoted `ct="110 112 60"`. | C §6.2.1 | MAY (c) | 1.1 | =REG-10 |
| REG-11 | The Server records the connection information of the Register (source IP:port or MSISDN) and uses it for all later interactions with that client. | C §6.2.1 | desc | 1.0 | =REG-11 |
| REG-12 | A Register from an already-registered client: the Server removes the old registration information and performs the new Register (factory-reset case). | C §6.2.1 | desc | 1.0 | =REG-12 |
| REG-13 | Lifetime expiry without Update: the Server MUST remove the registration and its observations. A later Update gets an error (4.04). The client SHOULD reset and MUST re-Register (Update is not enough). | C §6.2; T Tbl 6.7-2 | MUST | 1.0 | =REG-13 |
| REG-14 | Update = POST `/{location}?lt&b&Q&sms` with an optional payload. It MUST carry only the parameters of Tbl 6.2.2-1 (Lifetime, Binding, SMS Number, Objects, Profile ID) that changed. A parameter-less Update refreshes the lifetime. Same media type as Register. Codes: 2.04 / 4.00 / 4.04. | C §6.2.2; T §6.4.3 | MUST (c) | 1.0 | =REG-14 |
| REG-15 | An object list in an Update MUST follow the Register rules and is complete (it replaces, it is not a delta). The client MUST send an Update whenever a listed parameter changes. | C §6.2.2, Fig 6.2.2-2 | MUST (c) | 1.0 | =REG-15 |
| REG-16 | De-register = DELETE `/{location}`. Codes: 2.02 / 4.00 / 4.04. The Server removes the registration and assumes past states, including observations, are nullified. The client SHOULD De-register on shutdown; De-register support is SHOULD for the client. | C §6.2, §6.2.3, §6.4.1 note; T §6.4.3 | SHOULD (c) | 1.0 | =REG-16 |
| REG-17 | CoAP: when the IP address changes in NoSec mode the client MUST register again. The 1.1 rule "new security context ⇒ re-Register" was removed for CoAP in 1.2.1 but is still present for HTTP (T §7.1.3). | T §6.4.3, §1.2, §7.1.3 | MUST (c) | 1.1 (removed CoAP 1.2.1) | REG-17 Δ |
| REG-18 | Binding values: U, M (MQTT), H (HTTP), T, S, N, plus a separate `Q` flag (1.1+). 1.0 encoded queue mode inside `b` (UQ, SQ, UQS). Default U. `b` SHOULD equal /3/0/16. | C §6.2.1.2, Tbl 6.2.1-1 | SHOULD (c) | 1.0/1.1/1.2 | =REG-18 |
| REG-19 | Registration Update Trigger /1/x/8 (mandatory resource): on Execute the client MUST send an Update, using the current binding, /1/x/22 or the binding argument. Bootstrap-Request Trigger /1/x/9 (optional): the client MUST start Client Initiated Bootstrap. | E.2 res 8, 9; C §6.2.2 | MUST (c) | 1.0 / 1.1 | =REG-19 |
| REG-20 | Registration order and retry are client behaviour (/1/x/13-20). Defaults when absent (Tbl 6.2.1.1-1): priority undefined, failure block false, initial delay 0 s, bootstrap-on-failure true, retry count 5, retry timer 60 s, sequence delay 24 h, sequence retry count 1. Back-off = timer·2^(attempt−1). The server sees bursts of retried Registers. | C §6.2.1.1 | desc | 1.1 | =REG-20 |
| REG-21 | Profile ID: see the PROF group. | C §6.2.1 | — | 1.2 | =REG-21 → PROF |
| REG-22 | The Server MUST support the SMS Number parameter (part of "MUST support all parameters"). It matters for SMS binding and trigger. Format per 3GPP 23.003, with no leading "+". | C §6.2.1; T §6.4.3 note | MUST | 1.0 | =REG-22 |
| REG-23 | /1/x/1 Lifetime 0 = infinite lifetime: no expiry-based removal. | E.2 res 1 | desc | 1.0 | NEW |
| REG-24 | /1/x/4 Disable: after the Execute response the client MUST De-register, close the connection and ignore the server for /1/x/5 seconds (default 86400), then MUST Register again. | E.2 res 4, 5 | MUST (c) | 1.0 | NEW |
| REG-25 | /1/x/7 Binding: if the client supports the binding in this resource it MUST use it as the Current Binding Mode. The client SHOULD perform operations in the modes of /1/x/7. | E.2 res 7; C §6.2 | MUST/SHOULD (c) | 1.0 | NEW |
| REG-26 | /1/x/25 Supported Server Versions: multiple strings `1*DIGIT "." 1*DIGIT ["." 1*DIGIT]`, which tell the client which enabler versions the server supports. Writable by server or BS. | E.2 res 25 | MAY | 1.2 | NEW |
| REG-27 | Example flows. Register `POST /rd?ep=example-client&lwm2m=1.1&lt=86400` → `2.01 Location /rd/5a3f`; `POST /rd/5a3f?lt=600000` → 2.04; `DELETE /rd/5a3f` → 2.02. Vectors in `spec-examples.json` (`spec-uri-*`, `spec-lf-register-*`). | T Fig 6.4.3-1 | — | 1.1 | NEW |

### 1.3 PROF: Profile ID (registration compression)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| PROF-01 | Server support of Profile ID is optional: it is the only Register parameter excluded from "MUST support". The Server MAY refuse to use Profile ID by returning an error that forces the client to send the object list. | C §6.2.1 | MAY | 1.2 | REG-21 |
| PROF-02 | Query parameter `pid`. The value MUST be ASCII per the ABNF: `value = profileID / ( DQUOTE profileID *( "," profileID ) DQUOTE )`; `profileID = dynamicID / preConfigID`; `dynamicID = suite-identifier ":" 1*(DIGIT/"a"-"f")`; `suite-identifier = 1*DIGIT`; `preConfigID = ("oma"/"v") ":" 1*(ALPHA/DIGIT/"-")`. Hex is lowercase only. | C §6.2.1; T Tbl 6.4.3-1 | MUST | 1.2 | NEW |
| PROF-03 | The list a Profile ID stands for MUST NOT include /0, /21 or /23 (in both modes). | C §6.2.1 | MUST | 1.2 (/23 1.2.1) | NEW |
| PROF-04 | Pre-configured mode: `oma:<value>` is registered at OMNA, `v:<value>` is vendor-specific. The mapping is pre-provisioned on client and server. | C §6.2.1 | desc | 1.2 | NEW |
| PROF-05 | Dynamic mode: the client hashes the list of objects, object versions and instances, and puts `<suite>:<hex>` in pid. The suite is the RFC 6920 Named-Information hash suite ID (e.g. `6:1234abcd` = sha-256-32; RFC 6920 §9.4: 1=sha-256, 2=-128, 3=-120, 4=-96, 5=-64, 6=-32). The hash must be ≥32 bits. Object versions MUST always be included, even 1.0 and versions implied by the protocol version (1.2.2). | C §6.2.1; RFC 6920 §9.4 | MUST (c) | 1.2 (versions 1.2.2) | NEW |
| PROF-06 | If the Server cannot retrieve the list for a received Profile ID it MUST reply with an error. The CoAP code for "cannot determine the list" is 4.09 Conflict (T Tbl 6.7-2). The client MUST then Register again with the full list. | C §6.2.1; T Tbl 6.7-2 | MUST | 1.2 | NEW |
| PROF-07 | The client MAY send several Profile IDs (quoted comma list) and MAY mix them with a payload list. The Server uses all pids plus the payload to build the object set. | C §6.2.1 | MAY (c) | 1.2 | NEW |
| PROF-08 | With neither pid nor payload, the Server MAY take the list from pre-configuration (out of scope). If it cannot determine the list, the Server MUST reply with an error (4.09). | C §6.2.1 | MUST | 1.2 | NEW |
| PROF-09 | Profile ID is also an Update parameter, with the same rules (C Tbl 6.2.2-1), but the CoAP/HTTP Update URI templates omit `pid`. Accept `pid` on Update (A-9). | C §6.2.2; T Tbl 6.4.3-2 | MUST | 1.2 | NEW |
| PROF-10 | /1/x/27 Profile ID Hash Algorithm: the RFC 6920 suite ID the server prefers clients to use in dynamic mode (0..255). | E.2 res 27 | MAY | 1.2 | NEW |
| PROF-11 | Dynamic mode lets the Server learn pid → list mappings ("may be able to determine the mappings"). Recommended reading: cache a pid after a registration carrying both the pid and the full list. | C §6.2.1 | desc | 1.2 | NEW |

### 1.4 BS: Bootstrap interface (Bootstrap-Server obligations)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| BS-01 | Bootstrap-Server MUST support Client Initiated Bootstrap with Bootstrap-Request: `POST /bs?ep={ep}&pct={ct}`. Codes 2.04 / 4.00 (unknown ep, ep ≠ cert CN) / 4.15 (pct unsupported). | C §6.1; T §6.4.2 Tbl 6.4.2-1, Tbl 6.7-1 | MUST | 1.0 (pct 1.1) | =BS-01 |
| BS-02 | `pct` MUST be one of SenML JSON, SenML CBOR, LwM2M CBOR (1.2) or TLV. | C §6.1.7.1 | MUST (c) | 1.1 (LwM2M CBOR 1.2) | BS-02 Δ |
| BS-03 | Bootstrap-Write = PUT /o, /o/i, /o/i/r. The client MUST write regardless of existence or access rights. It may be repeated. Unknown optional resources MUST NOT be errors. The client MUST NOT instantiate optional resources missing from the payload (it MAY fill defaults). An object-level write MUST carry instances in TLV, LwM2M CBOR, SenML CBOR or SenML JSON. Codes 2.04 / 4.00 / 4.15. | C §6.1.7.5; T Tbl 6.4.2-1, 6.7-1 | MUST | 1.0 | =BS-03 |
| BS-04 | Bootstrap-Delete = DELETE /o/i, /o or `/`. It never touches the BS Account (its /0 instance plus associated /21, /23, /24 instances) or /3/0. Without an Object ID, all other instances MUST be removed. Codes 2.02 / 4.00. | C §6.1.7.6; T §6.4.2 | MUST (c) | 1.0 (/23,/24 1.2) | BS-04 Δ |
| BS-05 | Bootstrap-Discover = GET with Accept link-format on /o or `/`. Payload: `</>;lwm2m=X.Y`, object `ver`, the instance list. /0 instances carry `ssid` and `uri`; /1, /21, /23, /24 instances carry `ssid`, except BS-account instances, which carry none. /21 is reported if OSCORE is supported; /23 and /24 if MQTT is (1.2.1). Codes 2.05 / 4.00 / 4.04. | C §6.1.7.3; T Tbl 6.4.2-1 | desc | 1.1 (/23,/24 1.2.1) | BS-05 Δ |
| BS-06 | Bootstrap-Read = GET /1, /1/i, /2, /2/i only; any other object → error. Accept TLV, LwM2M CBOR, SenML CBOR, SenML JSON. The BS MUST NOT treat unknown optional resources in the reply as errors. Codes 2.05 / 4.00 / 4.01 / 4.04 / 4.05 / 4.06. The CoAP table says "'1' or '2'" since 1.2. The HTTP table still says "'2'" only (A-1). | C §6.1.7.4; T Tbl 6.4.2-1, 7.1.2-1 | MUST | 1.1 | =BS-06 |
| BS-07 | Bootstrap-Finish = POST /bs, empty payload. The BS MUST send it after the last instance or resource, or when it ends the bootstrap. It is not used with Bootstrap-Pack. Client replies 2.04 / 4.00 / 4.06 (inconsistent configuration). On error the BS MAY correct and re-issue Finish. | C §6, §6.1.3.3, §6.1.6; T §6.4.2, Tbl 6.7-1 | MUST | 1.0 | =BS-07 |
| BS-08 | Without a Finish within EXCHANGE_LIFETIME (247 s) the client MUST consider bootstrap failed (Bootstrap-Request flow only). | C §6.1.3.3, §6.1.6 | MUST (c) | 1.1 | =BS-08 |
| BS-09 | The BS uses only `/{o}/{i}/{r}` paths. Bootstrap SenML names exclude the alternate path. | T §6.4.1, §7.1.1 | MUST | 1.1 | =BS-09 |
| BS-10 | For full interoperability the BS MUST support bootstrapping with PSK, RPK and certificates (DTLS/TLS), and with PSK for OSCORE. PSK suites MUST NOT be used with low-entropy secrets or passwords. | T §5.2.4, §5.4.3 | MUST | 1.1 | =BS-10 |
| BS-11 | Server Initiated Bootstrap: an authorized Server executes /1/x/9 over an existing registration, subject to access rights. 1.1.1 SCR made this mandatory for servers (BOOT-005-S-M). In 1.2.x the SCR is void and /1/x/9 is an optional resource. | C §6.1.3.4; T §6.4.2 | desc | 1.1 | BS-11 Δ |
| BS-12 | Bootstrap-Pack-Request = GET `/bspack?ep={ep}&acc={BS-account instances}`, Accept SenML CBOR, SenML JSON or LwM2M CBOR, response 2.05 with the Pack. A BS MAY support it. If it does not, it MUST return "not implemented" (5.01). If it supports it but refuses, it MUST return "method not allowed" (4.05). Other codes: 4.00 / 4.01 / 4.04 / 4.06. After any error the client MUST use Bootstrap-Request. | C §6.1, §6.1.7.7; T §6.4.2 Tbl 6.4.2-1, 6.7-1 | MUST/MAY | 1.2 | =BS-12 |
| BS-13 | With OSCORE, a BS receiving Bootstrap-Request or Bootstrap-Pack-Request MUST use the Echo option. | T §6.4.2, §5.4.3 | MUST | 1.1 (Pack 1.2) | BS-13 Δ → OSC-04 |
| BS-14 | `acc` (1.2.1): BS-account object instances in CoRE link format. The target is the instance path and links MUST NOT carry parameters (`</0/0>`, `</0/2>,</21/0>`). | C §6.1.7.7 | MUST (c) | 1.2.1 | NEW |
| BS-15 | Bootstrap-Pack holds at least the required Bootstrap Information (≥1 Server Account). For each object in the Pack the client MUST replace all its instances, and MUST NOT delete objects absent from the Pack. Exceptions: the BS account (+/21) and /3/0. If the Pack is inconsistent or fails, the client MUST retry with Bootstrap-Request, so the BS must also serve the classic flow. On no response within EXCHANGE_LIFETIME the client MAY retry the Pack. | C §6.1.6, §6.1.7.7 | MUST (c) | 1.2 | NEW |
| BS-16 | After bootstrap, the Server Bootstrap Information MUST contain ≥1 LwM2M Server Account. The client has at most one BS Account. A Server Account = /0 instance (res 1 = false) + /1 instance paired by Short Server ID (/0/x/10 = /1/x/0), optionally linked via /0/x/17 (OSCORE), /0/x/26 (MQTT Server) and /0/x/27 (COSE). | C §3.2, §6.1.2 | MUST | 1.0 (links 1.1/1.2) | NEW |
| BS-17 | /0/x/10 MUST be set when /0/x/1 = false. SSID 0 and 65535 MUST NOT be used. | E.1 res 10; C Tbl 7.4-1 | MUST | 1.0 | ID-01 |
| BS-18 | Replacing the BS Account: replacement and purge MUST complete before the client answers Finish. Otherwise the client MUST answer 4.06 Not Acceptable and the old account stays active. | C §6.1.3.3 step 3 | MUST (c) | 1.1 | NEW |
| BS-19 | /0/x/12 BS-Account Timeout: the client MUST purge the BS account after it expires (minimum 1; 0 or absent = infinite). High-entropy, per-device BS keys SHOULD be used, and the account SHOULD then be kept (timeout 0 or absent). | C §6.1.3.3; E.1 res 12 | SHOULD | 1.1 | NEW |
| BS-20 | The client SHOULD send `ep` in Bootstrap-Request and Bootstrap-Pack-Request. It MAY omit it when it equals the security-protocol identifier and that identifier always reaches the BS (no proxies). | C §6.1.3.3, Tbl 6.1.7.1-1, 6.1.7.7-1 | SHOULD/MAY (c) | 1.1 | NEW |
| BS-21 | During bootstrap the client MAY ignore requests and flush pending responses not related to bootstrap. LwM2M Servers must not rely on in-flight exchanges then. | C §6.1.1; T §6.4.2 | MAY (c) | 1.0 | NEW |
| BS-22 | Client Initiated Bootstrap MAY be reused after the first bootstrap to update a few resources (all Bootstrap Information then OPTIONAL). | C §6.1.3.3 step 1 | MAY | 1.1 | NEW |
| BS-23 | If bootstrap deletes the Server Account of a registered server, the client MUST consider itself de-registered (the Server may get no De-register). | C §6.1.3.3 step 3 | MUST (c) | 1.1 | NEW |
| BS-24 | Per-mode /0 content. NoSec: res 3, 4, 5 SHALL be null. PSK: res 4 null. EST: res 3 and 5 null. The BS is RECOMMENDED to omit unused resources, and the client MUST ignore them if present. | E.1; T §5.2.9.1, §5.2.9.4, §5.2.9.5 | SHOULD | 1.1 | NEW |
| BS-25 | Object-level Create rights: an /2 instance with res 1 = 65535, owner 65535 and one ACL instance per authorized server with the C bit. It MUST only be created or updated during bootstrap. Without it no server can create instances of that object. | C §8.1.2.1, Tbl 8.1.2.1-1 | MUST | 1.0 | NEW |
| BS-26 | Moving from Access Control-Disabled to -Enabled (adding a second Server Account) MUST be done in a Bootstrap Phase. ACLs and owners set during bootstrap MUST keep the configuration consistent. ACL instance 0 = default rights. | C §8, §8.1.2.2, §8.1.3 | MUST | 1.0 | NEW |
| BS-27 | The BS MAY accept the legacy Bootstrap-Discover reply form where `lwm2m=1.0` has no URI-reference (`lwm2m=1.0,</0/0>;ssid=101,…`). | C §6.1.7.3 note | MAY | 1.2 | NEW |
| BS-28 | /0/x/0 Server URI is ≤255 chars, RFC 7252 §6 form. /0/x/25 Secondary Server URIs (1.2): the same server reachable over other schemes, e.g. `coaps` and `coaps+tcp`. | E.1 res 0, 25 | desc | 1.0 / 1.2 | NEW |
| BS-29 | Provisioned credentials MAY change at any time, even mid-(D)TLS session. Closing the session is a policy decision for client and server. | T §5.2.4 | MAY | 1.1 | NEW |
| BS-30 | /1/x/11 TLS-DTLS Alert Code (last alert from the server) and /1/x/12 Last Bootstrapped are client-maintained. The BS can use them to diagnose and refresh out-of-date accounts. | E.2 res 11, 12 | desc | 1.1 | NEW |

### 1.5 DM: Device Management & Service Enablement

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| DM-01 | "The LwM2M Server and the LwM2M Client MUST support all the operations on this interface unless clearly stated otherwise." Only the client is given MAY for Read-Composite and Write-Composite. With the SCR voided, the text alone makes the composites MUST for a 1.2.2 server (they were server-O in 1.1.1 SCR, A-3). | C §6.3, §6.3.8, §6.3.9 | MUST | 1.0 (composite 1.1) | DM-01 Δ |
| DM-02 | Read = GET /o, /o/i, /o/i/r, /o/i/r/ri, with optional Accept. An unsupported Accept MUST be rejected 4.06. An empty 2.05 is valid (object without instances). Codes 2.05 / 4.00 / 4.01 / 4.04 / 4.05 / 4.06. The client MUST error on a resource-instance ID on a single-instance resource or without a resource ID. | C §6.3.1; T §6.4.4, Tbl 6.7-3 | MUST | 1.0 (RI 1.1) | =DM-02 |
| DM-03 | Unsupported or unknown optional resources in Read, Bootstrap-Read and Send payloads MUST NOT be errors for the Server. 1.2.2 removed this sentence for Discover; still tolerate it there. | C §6.3.1, §6.1.7.4, §6.4.6 | MUST | 1.1 | DM-03 Δ (1.2.2) |
| DM-04 | Discover = GET with Accept link-format, `?depth=0..3` (1.2). Default depth 2 for an object target, 1 otherwise (Tbl 6.3.2-2). The client SHOULD support depth, and if it does not it MUST ignore it and use the default. The client MUST return the instantiated tree to that depth with attached or inherited attributes (1.2.2), only those of the requesting server (resource-instance level too, 1.2.2), `dim` on multi-instance resources, and `ver` per §7.2.3. Codes 2.05 / 4.00 / 4.01 / 4.04 / 4.05. Object-level Discover needs no access right. | C §6.3.2, §8.2.3; T §6.4.4 | MUST (c) | 1.0 (depth 1.2) | DM-04 Δ |
| DM-05 | Write: PUT = Replace on /o/i, /o/i/r, /o/i/r/ri. POST = Partial Update on /o/i, or on /o/i/r for multi-instance resources. Content-Format MUST be present. POST formats MUST be TLV, LwM2M CBOR, SenML CBOR or SenML JSON. Writing more than one value, or deleting or allocating resource instances, MUST use TLV, LwM2M CBOR or SenML. Client and Server MUST support both Replace and Partial Update. Object-level Write is not supported over CoAP (indistinguishable from Create). | C §6.3.3; T §6.4.4, Tbl 6.4.4-1 | MUST | 1.0 | DM-05 Δ |
| DM-06 | Write is rejected by the client for: unsupported format (4.15); a value of the wrong format; out of range; an Objlnk that is not `oid:65535` of a present object, an existing instance, or `65535:65535`; disallowed creation or deletion of resource instances (4.01); any non-writable resource in the payload (method not allowed). Codes 2.04 / 2.31 / 4.00 / 4.01 / 4.04 / 4.05 / 4.06 / 4.08 / 4.13 / 4.15. Unknown optional resources in an instance write are not errors. | C §6.3.3; T §6.4.4, Tbl 6.7-3 | MUST (c) | 1.0 | =DM-06 |
| DM-07 | Write-Attributes = PUT `path?pmin=&pmax=&gt=&lt=&st=&epmin=&epmax=&edge=&con=&hqmax=`. Only <NOTIFICATION> attributes are writable. Several may be sent at once, and they MUST be consistent or the client answers 4.00. An attribute without a value MUST be used to unset it at that level. Values MUST be server-specific. Codes 2.04 / 4.00 / 4.01 / 4.04 / 4.05. Needs R access (Tbl 8.2-1) except at object level. | C §6.3.4, §8.2.3; T §6.4.4 | MUST | 1.0 (ep* 1.1, edge/con/hqmax 1.2) | DM-07 Δ |
| DM-08 | Execute = POST /o/i/r only; on an instance or resource instance the client MUST return an error. A non-executable resource gives 4.05. Arguments (optional) MUST be text/plain per `arglist = arg *("," arg); arg = DIGIT / DIGIT "=" "'" *CHAR "'"; CHAR = "!" / %x23-26 / %x28-5B / %x5D-7E`, and each DIGIT MUST appear at most once (1.2.1). Content-Format none or text/plain. MAY be NON. Codes 2.04 / 4.00 / 4.01 / 4.04 / 4.05. | C §6.3.5; T §6.4.4 | MUST | 1.0 (unique 1.2.1) | =DM-08 |
| DM-09 | Create = POST /o, formats TLV, LwM2M CBOR, SenML CBOR or SenML JSON; Content-Format MUST be set. With no instance reference (TLV without an OI wrapper; LwM2M CBOR instance 65535, 1.2.1) the client MUST assign the ID and return it in 2.01 (Location). Single-instance object → ID 0. The object MUST have been announced in Register/Update. All mandatory resources must end up instantiated, else error. Read-only values in the payload MUST be ignored without error. An instance ID conflict MUST reject the whole request as a bad request. On any error nothing is created. The client MAY reject. Codes 2.01 / 4.00 / 4.01 / 4.04 / 4.05 / 4.06 / 4.15. | C §6.3.6; T §6.4.4, Tbl 6.7-3 | MUST | 1.0 (CBOR 1.2) | DM-09 Δ |
| DM-10 | Delete = DELETE /o/i or /o/i/r/ri (1.1). The target MUST be an announced instance. /3/0 SHALL NOT be deleted. The client MAY reject. Codes 2.02 / 4.00 / 4.01 / 4.04 / 4.05. | C §6.3.7; T Tbl 6.4.4-1 | MUST | 1.0 (RI 1.1) | =DM-10 |
| DM-11 | The client MUST reject any DM/SE or IR operation on /0, /21 or (1.2) /23 with "not authorized" (4.01). Their resources are also bootstrap-only (empty Operations, D.1). | C §6.3, §6.4; E.1, E.9 | MUST (c) | 1.0/1.1/1.2 | DM-11 Δ |
| DM-12 | Read-Composite = FETCH on `/`. Body is SenML-ETCH JSON/CBOR (320/322) or SenML JSON/CBOR (110/112, re-added in 1.2.1). Accept LwM2M CBOR, SenML CBOR or SenML JSON. Non-atomic, best-effort: missing values are omitted. Write-Composite = iPATCH on `/` with LwM2M CBOR, SenML CBOR/JSON or SenML-ETCH CBOR/JSON. Atomic: the client MUST check existence and W rights on all targets first and reject everything if one fails. Resources not listed are untouched. Codes per Tbl 6.7-3 (Read-Composite adds 4.15). | C §6.3.8, §6.3.9; T Tbl 6.4.4-1, 6.7-3 | MUST | 1.1 (ETCH/LwM2M CBOR 1.2) | DM-12 Δ |
| DM-13 | Object-level rules: Create needs C on the object's /2 instance. Discover needs no right. Read and Observe aggregate the readable instances only. Write-Attributes is always performed. | C §8.2.3 | MUST (c) | 1.0 | =DM-13 |
| DM-14 | Access control is enforced by the client (/2). The Server must handle 4.01 and may manage ACLs as Access Control Owner. | C §8, §8.2 | desc | 1.0 | =DM-14 |
| DM-15 | Minimum access right per operation: Read, Read-Composite, Observe, Observe-Composite, Write-Attributes → R. Write, Write-Composite → W. Discover → none. Delete → D. Execute → E. Create → C on the object. Allowed operations on a resource apply to all its instances. | C §8.2 Tbl 8.2-1 | MUST (c) | 1.0 | NEW |
| DM-16 | Access right resolution (AC-Enabled): (1) the server is owner with no own ACL → full rights; (2) its own ACL instance; (3) otherwise the ACL instance 0 default; (4) otherwise none. Write-Composite needs W on every instance or nothing is granted. Read-/Observe-Composite skip non-readable instances and succeed if at least one is readable. | C §8.2.1, §8.2.2 | MUST (c) | 1.0 (composite 1.1) | NEW |
| DM-17 | When a Server Creates an instance (AC-Enabled) the client creates an /2 instance with owner = that server's SSID and full rights. Only the owner may manage that /2 instance via DM. An owner Write creates or sets `ACL[ssid]`. Deleting the instance via the owner deletes its /2 instance. | C §6.3.6, §8.1.2.2 | MUST (c) | 1.0 | NEW |
| DM-18 | /2 ACL bits: bit0 R (Read, Observe, Write-Attributes), bit1 W, bit2 E, bit3 D, bit4 C; other bits reserved. Resource-instance ID = SSID; instance 0 = default. Owner 65535 = bootstrap-managed. Ranges: OID 1..65534, OIID 0..65535, ACL 0..31. | E.3 Tbl E.3-2 | MUST | 1.0 | NEW |
| DM-19 | Unbootstrapping (Bootstrap-Delete of a /0 instance): the client deletes the paired /1 instance (MAY De-register), deletes instances only that server could access, removes its ACL entries, hands ownership to the server with the highest W+D sum, and deletes that server's observations. A Server MAY observe /2/x/3 to follow owner changes. | T §5.2.5, §5.4.4 | MUST (c)/MAY | 1.1 | NEW |
| DM-20 | Replace on a multi-instance resource replaces the whole array (if authorized). Partial Update creates or overwrites instances but cannot delete them (C §6.3.3.1 example `/34/0/1`). | C §6.3.3, §6.3.3.1 | MUST | 1.0 | NEW |

### 1.6 ATT: attributes

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| ATT-01 | The Server MUST support every <NOTIFICATION> attribute: pmin, pmax, gt, lt, st, epmin, epmax, edge, con, hqmax. Server, BS and client SHOULD support the <PROPERTIES> attributes dim, ssid, uri, ver, lwm2m. Per Tbl 7.3.1-1 "Support Required": `ver` YES (Server), `lwm2m` YES (Bootstrap-Server). | C §7.3.1, §7.3.2 | MUST/SHOULD | 1.0 (1.2.1 rewrite) | ATT-01 Δ |
| ATT-02 | Inheritance: an attribute applies at its level and below. Precedence: a lower level overrides a higher one. Defaults: pmin = /1/x/2 (0 if absent), pmax = /1/x/3 (else 0, meaning none). | C §7.3.2; E.2 res 2, 3 | MUST (c) | 1.0 | =ATT-02 |
| ATT-03 | pmin: the client MUST wait at least pmin seconds between notifications. A change that arrives inside pmin MUST be notified as soon as pmin expires. | C Tbl 7.3.2-1 | MUST (c) | 1.0 | =ATT-03 |
| ATT-04 | pmax: a notification MUST be sent when pmax expires after the last one. 0 or absent ⇒ MUST be ignored. pmax MUST be ≥ pmin, otherwise it is ignored for that resource. | C Tbl 7.3.2-1, §6.4.2 | MUST (c) | 1.0 (≥ 1.2.1) | =ATT-04 |
| ATT-05 | gt and lt: notify on each crossing of the threshold (subject to pmin). st: notify when the change since the last notification is ≥ st. Numeric readable resources and resource instances only. | C Tbl 7.3.2-1 | MUST (c) | 1.0 | =ATT-05 |
| ATT-06 | Required: lt < gt and lt + 2·st < gt. A Write-Attributes, or (1.2) Observe / Observe-Composite parameters, violating this MUST be rejected. | C §7.3.2; T §6.4.4 | MUST | 1.0 (Observe 1.2) | ATT-06 Δ |
| ATT-07 | epmin/epmax: after epmin the client MAY evaluate; after epmax it MUST evaluate. Absent = undefined. epmin < epmax (lowercase "must"). | C §7.3.2, Tbl 7.3.2-1 | MUST (c) | 1.1 | =ATT-07 |
| ATT-08 | edge ("0" falling, "1" rising) on Boolean resources or resource instances. con ("0"/"1"): CON if any part of the notification has con=1. hqmax: number of historical entries stored while offline or disabled; drop the oldest when full; only parts with hqmax>0 are included; needs /1/x/6 = true. | C Tbl 7.3.2-1 | MUST (c) | 1.2 | =ATT-08 |
| ATT-09 | <NOTIFICATION> behaviour MUST follow draft-ietf-core-conditional-attributes (May 2022) [Cond_Attr] unless this spec says otherwise. hqmax is not in Cond_Attr; Cond_Attr's c.band is not used. | C §7.3.2 | MUST | 1.2 | NEW |
| ATT-10 | Syntax and attachment. pmin, pmax, epmin, epmax, hqmax = `"=" 1*DIGIT` at Object, Instance, Resource or Resource-Instance level. gt, lt, st = `"=" 1*DIGIT ["." 1*DIGIT]` at Resource or Resource-Instance level only, numeric. edge only on Boolean. con = 0/1 at any level. All RW. | C Tbl 7.3.2-1 | MUST | 1.0-1.2 | NEW |
| ATT-11 | <PROPERTIES> are read-only (R) and exposed in Discover. `dim` 0..65535 on multi-instance resources. `ssid` 1..65534 on /0, /1, /21 instances. `uri` quoted string on /0 instances. `ver` = `1*DIGIT "." 1*DIGIT`, default 1.0. `lwm2m` only on the root link of a Bootstrap-Discover reply. | C §7.3, Tbl 7.3.1-1 | desc | 1.1 | NEW |
| ATT-12 | Changing attributes during an observation has implementation-dependent effect. For deterministic behaviour the Server SHOULD cancel and re-create the observation. | C §6.4.2 note | SHOULD | 1.1 | NEW |

### 1.7 OBS / SEND: information reporting

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| OBS-01 | Observe = GET with Observe=0 on /o, /o/i, /o/i/r or /o/i/r/ri, optionally with `?pmin&pmax&gt&lt&st&epmin&epmax&edge&con&hqmax` (1.2). The response MUST be treated as the initial notification. Codes 2.05 / 4.00 / 4.01 / 4.04 / 4.05 / 4.06. A resource without Read → the client MUST answer method-not-allowed. | C §6.4.1, §6.4.2; T §6.4.5 Tbl 6.4.5-1, 6.7-4 | MUST | 1.0 (attrs 1.2) | OBS-01 Δ |
| OBS-02 | Cancel Observation = GET with Observe=1 on the same path, or RST in reply to a notification (T Fig 6.4.5-1). | C §6.4.3; T §6.4.5 | MUST | 1.0 | =OBS-02 |
| OBS-03 | The Server MUST re-initiate every observation it wants whenever the client Registers. A client that forgot its observations MUST Register again. The client SHOULD keep observations across reboots. | C §6.4.1 | MUST | 1.0 | =OBS-03 |
| OBS-04 | Notify carries the new value (all readable resources for an object or instance observation). It MAY be NON. The client MUST NOT notify without Read rights. Each notify restarts the pmin and pmax timers. | C §6.4.2, §8.2.4; T §6.1 | MUST (c) | 1.0 | =OBS-04 |
| OBS-05 | Observe-Composite = FETCH on `/` with Observe=0. Body SenML-ETCH JSON/CBOR or SenML JSON/CBOR; Accept LwM2M CBOR, SenML CBOR or SenML JSON. Optional query `pmin, pmax, epmin, epmax, con` (object-attachment-level attributes only, MUST). When present, the attached or inherited object-level attributes MUST be ignored. Every notification carries every listed resource. Cancel = FETCH Observe=1 with exactly the same list. A client that supports it MUST support cancel. The Server must keep pmin/pmax consistent across targets. Codes add 4.15. | C §6.4.4, §6.4.5; T Tbl 6.4.5-1 | MUST | 1.1 (attrs 1.2) | OBS-05 Δ |
| OBS-06 | Observation Attributes on Observe override the attached ones for that observation only and do not appear in Discover. The client SHOULD support them; if it does not, it MUST reject the Observe. | C §6.4.1 | SHOULD/MUST (c) | 1.2 | =OBS-06 |
| OBS-07 | Notify MUST be sent when the state changes and all conditions hold, or when pmax expires. The conditions come from the observation parameters or from attributes at the observed level. Attributes attached below the observed level MUST be ignored. | C §6.4.2 | MUST (c) | 1.0 (1.2.1 rewrite) | NEW |
| OBS-08 | /1/x/6 Notification Storing When Disabled or Offline (default true): stored notifications are sent later, possibly as historical SenML records with `t`. The Server must accept late, batched, time-stamped notifications. | E.2 res 6; C §7.5.6 | desc | 1.0 | NEW |
| OBS-09 | /1/x/26 Default Notification Mode: 0 NON, 1 CON. | E.2 res 26 | MAY | 1.2 | NEW |
| OBS-10 | De-register, re-Register and unbootstrap make previous observations void (C §6.4.1 note; T §5.2.5 step 4). | C §6.4.1; T §5.2.5 | desc | 1.0 | NEW |
| SEND-01 | Send = POST `/dp` from the client. Content-Format MUST be set, and MUST be LwM2M CBOR, SenML JSON or SenML CBOR. Client and Server MAY support Send. Server replies 2.04 / 4.00 / 4.04 (object not registered). MAY be NON (1.2.1). | C §6.4.6; T §6.4.5, Tbl 6.7-4, §6.8.1 | MAY/MUST | 1.1 | SEND-01 Δ |
| SEND-02 | Reported instances MUST have been registered, and the Server must have Read rights. If any rule is broken the Server MUST answer with an error. Unknown optional resources MUST NOT be errors. /1/x/23 Mute Send: true or absent ⇒ Send disabled (the default is muted); the Server MAY write false to enable it. | C §6.4.6; E.2 res 23 | MUST | 1.1 | =SEND-02 |
| SEND-03 | Gateway: Send MAY also carry Device Object instances (prefixed) that were never registered. | GW §8.3.3 | MUST (tolerate) | GW 1.1 | NEW |

### 1.8 QM: queue mode

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| QM-01 | The Server MUST support Queue Mode, on CoAP and on HTTP. The client SHOULD. 1.1+ signals it with `Q`; 1.0 inside `b`. | T §6.5, §7.2 | MUST | 1.0 | =QM-01 |
| QM-02 | While the client is offline the Server holds downlink requests. It sends them after receiving a message from the client (a CON Update on CoAP, an Update on HTTP), one at a time (NSTART=1, wait for each response). | T §6.5, §7.2 | desc | 1.0 | =QM-02 |
| QM-03 | The client is RECOMMENDED to stay awake MAX_TRANSMIT_WAIT after its last message. (Informative App. D.8.3 instead says the client "MUST wait at least ACK_TIMEOUT", A-14.) | T §6.5; T D.8.3 | SHOULD (c) | 1.0 | =QM-03 |
| QM-04 | If CoAP retransmission fails, the Server has to inform the application (API out of scope). | T §6.5 | desc | 1.0 | =QM-04 |
| QM-05 | After sleep the IP or port may change, so the client re-runs the (D)TLS handshake (resumption RECOMMENDED). With CID no handshake is needed (CID group). | T §6.5 step 3 | SHOULD (c) | 1.0 | =QM-05 |
| QM-06 | Firewalls MUST allow outgoing traffic to 5683/5684 and incoming return traffic for ≥240 s. Where firewalls cannot be changed, CoAP over TCP/TLS SHOULD be used. | T §6.2, §6.3 | MUST/SHOULD | 1.1 | =QM-06 |
| QM-07 | The Server must accept responses piggybacked in the ACK, or as separate CON or NON messages. | T §6.5 | desc | 1.0 | NEW |

### 1.9 SEC: (D)TLS-based security

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| SEC-01 | Server and BS MUST authenticate the client before any data exchange, and MUST encrypt and integrity-protect everything sent to it. The security solution MUST provide replay protection, secure request/response binding and separately verifiable fragments (FOTA). This also applies through LwM2M-aware and -unaware intermediaries. | T §5.1 | MUST | 1.0 | =SEC-01 |
| SEC-02 | The client is always the (D)TLS client; Server and BS are always (D)TLS servers. | T §5.2.7 | MUST | 1.1 | =SEC-02 |
| SEC-03 | Each mode is now conditional: "If a LwM2M Server supports [PSK / RPK / X.509] it MUST support …". No 1.2.2 text makes any mode mandatory for the Server (the 1.1.1 SCR did: SEC-002/003/004/005/006-S-M). The BS still MUST support all three (BS-10). Recommended reading: support all three (A-4). | T §5.2.9.1-3 | MUST (cond.) | 1.2 | SEC-03 Δ |
| SEC-04 | PSK (if supported): the Server MUST support TLS_PSK_WITH_AES_128_CCM_8 (0xC0A8) and TLS_PSK_WITH_AES_128_CBC_SHA256 (0x00AE). The client SHOULD NOT use CBC. 1.1 and 1.2 clients MUST support CCM_8. | T §5.2.9.1 | MUST | 1.0 | =SEC-04 |
| SEC-05 | PSK mode: /0/x/2 = 0. /0/x/3 = PSK identity, /0/x/5 = PSK. Client and Server MUST support identities up to 128 bytes and keys up to 64 bytes; ≥16-byte keys RECOMMENDED. /0/x/4 MUST NOT be used (the BS SHOULD omit it, the client ignores it). | T §5.2.9.1 | MUST | 1.0 | =SEC-05 |
| SEC-06 | The Server MUST compare `ep` in Register, Bootstrap-Request and (1.2) Bootstrap-Pack-Request with the identity from the (D)TLS handshake (equality or lookup table), and MUST answer 4.00 on mismatch. `ep` MUST NOT be used alone for decisions. | T §5.2.6, §5.2.9.3; C §7.4.1 | MUST | 1.0 (Pack 1.2) | SEC-06 Δ |
| SEC-07 | LwM2M MUST NOT be deployed without appropriate security. NoSec (/0/x/2 = 3) requires alternative mechanisms (MUST). The Server MUST compare `ep` (Register, Bootstrap-Request, Bootstrap-Pack-Request) with the network-access identity. The BS SHOULD omit /0/x/3, 4, 5. | T §5.2.9.4 | MUST | 1.0 | =SEC-07 |
| SEC-08 | /0/x/2 modes: 0 PSK, 1 RPK, 2 Certificate, 3 NoSec, 4 Certificate with EST. Resource use per Tbl 5.2.4-1. | T §5.2.4; E.1 | MUST | 1.0 (4: 1.1) | =SEC-08 |
| SEC-09 | RPK (if supported): the Server MUST support TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 (0xC0AE) and _AES_128_CBC_SHA256 (0xC023). /0/x/3 = client SPKI (RFC 7250), /0/x/5 = OneAsymmetricKey (RFC 5958), /0/x/4 = server SPKI. The TLS server MUST store its key pair and a copy of the expected client key, and MUST check an exact match. Suites SHOULD be ECDSA + ECDHE. | T §5.2.9.2 | MUST | 1.0 | =SEC-09 |
| SEC-10 | X.509 (if supported): X.509v3, SHOULD follow the RFC 7925 profile; same two suites MUST. /0/x/3 = client end-entity cert plus optional chain; DER MUST be supported by client and BS, PEM MAY (enables several certs, 1.2.1). /0/x/5 = DER OneAsymmetricKey v1 (v2 and encrypted keys unsupported). /0/x/4 = trust anchor or the server cert itself (domain-issued). Client certs SHOULD put `ep` in the subject CN, and the Server MUST run the SEC-06 check. | T §5.2.9.3 | MUST | 1.0 (PEM 1.2.1) | SEC-10 Δ |
| SEC-11 | BS, Server and client MUST use different key pairs. Client keys MUST be unique per client. Different credentials per server are recommended. Both TLS ends SHOULD keep security state as long as safely possible (secure storage across sleep). | T §5.2.1 | MUST/SHOULD | 1.0 | =SEC-11 |
| SEC-12 | Alert handling (Tbl 5.2.10-1). Retry: 0, 10, 20, 21, 22, 40, 47, 50, 51, 70, 71, 86, 90, 100, 111. Fail: 42-46, 48, 49, 112-115. Ignore: 30, 41, 60, 110. The client MUST treat Fail as unrecoverable and Retry as possibly transient, following /1 error-handling resources. DTLS SHOULD silently discard bad-MAC records. | T §5.2.10 | MUST (c)/SHOULD | 1.1 | =SEC-12 |
| SEC-13 | TLS 1.3, DTLS 1.3 and CID: see the TLS13 and CID groups. | T §5.2.1, §5.2.8 | — | 1.2 | =SEC-13 → TLS13/CID |
| SEC-14 | Deployments without DNS (IP-literal URI): TLS/DTLS client and server MUST support SNI (RFC 6066). /0/x/14 SNI resource: the server picks its cert from SNI, and SNI becomes the client's reference identifier. | T §5.2.9.6.1; E.1 res 14 | MUST | 1.1 | =SEC-14 |
| SEC-15 | OSCORE: see the OSC group. | T §5.4 | — | 1.1 | =SEC-15 → OSC |
| SEC-16 | ECDHE group secp256r1 SHALL be supported. ECDSA curve secp256r1 SHALL be supported. Curves under 255 bits SHALL NOT be supported. | T §5.2.3, §5.2.9.3 | MUST | 1.1 | NEW |
| SEC-17 | Implementations SHOULD conform to RFC 7925. Additional state-of-the-art ciphersuites MAY be supported. | T §5.2.1, §5.2.2 | SHOULD/MAY | 1.1 | NEW |
| SEC-18 | The client MUST implement RFC 6125 / RFC 7925 §4.4.1 service-identity matching against the server cert. A server provisioned with an FQDN MUST present that FQDN in its cert (SAN or CN). Skipped if the cert equals /0/x/4. | T §5.2.9.3 | MUST | 1.1 | NEW |
| SEC-19 | /0/x/13 Matching Type: 0 exact (default), 1 SHA-256, 2 SHA-384, 3 SHA-512 of the server key or cert. /0/x/15 Certificate Usage per RFC 6698: 0 CA constraint, 1 service cert constraint, 2 trust anchor assertion, 3 domain-issued (default). The Server's credential must satisfy what the BS provisions. | E.1 res 13, 15; T §5.2.9.6.4 | MUST | 1.1 | NEW |
| SEC-20 | Certificate expiry: the BS or an authorized Server MAY write /3/0/13 to set the client's time, and the client SHOULD verify freshness. Revocation recovery may use server-initiated bootstrap. There is no in-band recovery for an expired or revoked BS cert. | T §5.2.9.6.2-3 | MAY | 1.1 | NEW |
| SEC-21 | /0 MUST only be changed by a BS or Smartcard bootstrap and MUST NOT be accessible to any other server (DM-11). One /0 instance SHOULD address a BS. | E.1 | MUST | 1.0 | NEW |

### 1.10 TLS13: TLS 1.3 / DTLS 1.3 and handshake configuration

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| TLS13-01 | 1.2 MAY optionally use TLS 1.3 (RFC 8446) and/or DTLS 1.3 (RFC 9147). | T §5.2.1 | MAY | 1.2 | SEC-13 |
| TLS13-02 | /0/x/16 DTLS/TLS Ciphersuite (multiple, unsigned): the suites the client must propose, each as a 32-bit value from the two IANA bytes (0xC0,0xA8 → 0xc0a8 = 49320). The BS must only provision suites the Server accepts (derived). | E.1 res 16 | MUST (c) | 1.1 | NEW |
| TLS13-03 | /0/x/18 Groups To Use by Client, ordered most- to least-preferred (instance 0 first), RFC 8446 §4.2.7 codes (secp256r1 = 0x0017). | E.1 res 18 | desc | 1.2 | NEW |
| TLS13-04 | /0/x/19 Signature Algorithms Supported by Server and /0/x/21 Signature Algorithm Certs Supported by Server (RFC 8446 §4.2.3, e.g. ecdsa_secp256r1_sha256 0x0403). /0/x/20 Signature Algorithms To Use by Client (ordered). The BS must provision values that match the Server's real support. | E.1 res 19-21 | desc | 1.2 | NEW |
| TLS13-05 | /0/x/22 TLS 1.3 Features To Use by Client (bitmask, 0 = do not use): bit0 PSK plain, bit1 0-RTT, bit2 PSK with PFS, bit3 certificate-based authentication. Bits 4-31 reserved. | E.1 res 22 | desc | 1.2 | NEW |
| TLS13-06 | /0/x/23 Extensions Supported by Server and /0/x/24 Extensions To Use by Client (bitmaps): bit0 SNI, 1 Max Fragment Length, 2 Status Request, 3 Heartbeat, 4 ALPN, 5 Signed Cert Timestamp, 6 Certificate Compression, 7 Record Size Limit, 8 Ticket Pinning, 9 Certificate Authorities, 10 OID Filters, 11 Post-Handshake Auth, 12 Connection ID. Bits 13-31 reserved. | E.1 res 23, 24 | desc | 1.2 | NEW |

### 1.11 CID: DTLS Connection ID

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| CID-01 | DTLS 1.2 CID (RFC 9146) and the DTLS 1.3 CID let a client keep its DTLS association across NAT rebinding and long sleep. **No BCP 14 keyword: CID support is optional for client and server in 1.2.2.** | T §5.2.8 | desc | 1.2 | SEC-13 Δ |
| CID-02 | Bit 12 "Connection ID" in /0/x/23 (Server supports) and /0/x/24 (client should use). The cited reference is the draft, `draft-ietf-tls-dtls-connection-id` / `draft-ietf-tls-dtls13`, while T §2.1 cites RFC 9146 (A-16). | E.1 res 23, 24 | desc | 1.2 | NEW |
| CID-03 | The Server uses the registration's recorded address for all later traffic (REG-11). With CID, recommended reading: follow the RFC 9146 §6 peer-address update, so a rebind is neither a new registration nor (per REG-17) a re-Register trigger (derived). | C §6.2.1; T §6.5 | desc | 1.2 | REG-11, QM-05 |

### 1.12 OSC: OSCORE

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| OSC-01 | OSCORE is optional. It MAY protect client↔BS and client↔Server traffic, and MAY run end-to-end to a non-LwM2M endpoint through a Server (both ends MUST implement it and hold a context; the mapping is out of scope). | T §5.4.1 | MAY | 1.1 | NEW |
| OSC-02 | AEAD AES-CCM-16-64-128 (mandated by RFC 8613) and HKDF with HMAC-SHA-256 by default. Other algorithms MAY be supported. | T §5.4.2 | MUST | 1.1 | NEW |
| OSC-03 | The BS MUST support OSCORE bootstrapping with a PSK. The PSK MUST be high-entropy and unique per device and per protocol. RFC 8613 §3.3 reuse conditions MUST be met. Implementations SHALL follow RFC 8613 Appendix B.2 on first use of a context. | T §5.4.3 | MUST | 1.1 | NEW |
| OSC-04 | The receiving endpoint MUST use Echo when an OSCORE context is first used (Bootstrap-Request, Bootstrap-Pack-Request, Register). With OSCORE, Echo MUST be used at least on the first registration operation. Echo SHOULD also be used for operations needing freshness (e.g. Write). Rejecting an initial request = 4.01 (Tbl 6.7-2). | T §5.4.3, §6.4.2, §6.4.3, Tbl 6.7-2 | MUST | 1.1 | BS-13, SEC-15 |
| OSC-05 | `ep` MAY be authenticated by setting /21/x/1 Sender ID = ep. Otherwise the Server MUST compare ep (Register, Bootstrap-Request, Bootstrap-Pack-Request) with the client's Sender ID (equality or lookup) and MUST answer 4.00 on mismatch. | T §5.4.5 | MUST | 1.1 | =SEC-15 |
| OSC-06 | OSCORE roles follow CoAP roles. Client Sender ID = Server Recipient ID and the other way round. On DM requests the Server is the OSCORE client. | T §5.4.6 | desc | 1.1 | NEW |
| OSC-07 | /0/x/17 OSCORE Security Mode MUST link to a /21 instance. /21/x/0 Master Secret, /21/x/1 Sender ID and /21/x/2 Recipient ID are MUST, stored "as an UTF-8 string" although typed Opaque (A-18). Optional: /21/x/3 AEAD (RFC 8152 Tbl 10), /x/4 HMAC (Tbl 7), /x/5 Master Salt, /x/6 ID Context. A /21 instance MUST NOT be linked from more than one /0 instance. | T §5.4.7.1; E.9 | MUST | 1.1 (res 6: /21 v2.0) | NEW |
| OSC-08 | OSCORE plus /0/x/2 0-2 = both DTLS and OSCORE. OSCORE plus mode 3 = OSCORE only. SMS NoSec with /0/x/17 present = SMS protected by OSCORE. | T §5.4.1, §5.3.1 | desc | 1.1 | NEW |
| OSC-09 | OSCORE can be carried in HTTP via the `OSCORE` header field, including through HTTP↔CoAP proxies (RFC 8075). | T §5.4.1 | MAY | 1.1 | NEW |

### 1.13 EST: certificate mode with EST

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| EST-01 | /0/x/2 MUST be 4 and the server cert MUST be in /0/x/4. The client generates its key pair locally, runs EST-coaps (RFC 9148) to get a cert, then behaves as certificate mode. | T §5.2.9.5 | MUST | 1.1 | SEC-08 |
| EST-02 | With EST-coaps for bootstrapping, Simple PKI messages (simpleenroll/simplereenroll) and CA-certificate retrieval SHALL be supported. CSR Attributes and server-generated keys are not required. The EST service is reached via the BS ("provisioning certificates from the LwM2M Bootstrap-Server"). | T §5.2.9.5 | MUST | 1.1 | NEW |
| EST-03 | /0/x/3 and /0/x/5 are unused: the BS is RECOMMENDED to omit them, the client MUST ignore them, and E.1 says they SHALL be null. The private key SHOULD never leave the device. | T §5.2.9.5; E.1 | SHOULD | 1.1 | NEW |

### 1.14 TCP: CoAP over TCP / TLS / WebSockets

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| TCP-01 | CoAP over TCP/TLS (RFC 8323) SHOULD be used for better firewall and NAT traversal. Server support is optional (C §6.2.1.2). Binding letter T; when T is current the Server MUST send requests over it (GEN-12). | T §6.1, §6.2; C §6.2.1.2 | SHOULD | 1.1 | NEW |
| TCP-02 | Schemes and default ports: `coap+tcp://` 5683 (NoSec), `coaps+tcp://` 5684 (TLS). | T §6.8.2 | MUST | 1.1 | NEW |
| TCP-03 | CoAP over WebSockets (RFC 8323) MAY be used: `coap+ws://` port 80, `coaps+ws://` port 443. | T §6.1, §6.8.6 | MAY | 1.2.1 | NEW |
| TCP-04 | Block-interchange and freshness attacks do not apply on connection-oriented transport (Request-Tag and Echo matter less there). | T §5.5, §5.6 | desc | 1.1 | NEW |

### 1.15 SMS

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| SMS-01 | CoAP over SMS: the CoAP message goes in the SMS payload with 8-bit encoding. Concatenation MAY be used above 140 characters. CoAP retransmission is disabled. The client signals the binding with the `sms` parameter (MSISDN). | T §6.8.3 | MAY | 1.0 | NEW |
| SMS-02 | Trigger: when the current binding is not S and /1/x/21 Trigger exists and is true (default false), the Server MAY send an Execute over SMS (/1/x/8 Update Trigger, /1/x/9 Bootstrap Trigger). The client MUST NOT respond, and MUST ignore any other operation over SMS. | T §6.6, §6.6.1, §6.6.2; E.2 res 21; C §6.2.1.2 | MAY | 1.1 | NEW |
| SMS-03 | SMS Secured mode MUST be supported when the SMS binding is used. Any client, Server or BS on SMS MUST discard SMS not protected with the expected /0/x/7 parameters (KIc, KID, SPI, TAR, KIc = byte 0, bit order per ETSI 102 221) and /0/x/8 keys (16, 32 or 48 bytes), and MUST NOT reply with a correctly secured error. | T §5.3, §5.3.2; E.1 res 6-8 | MUST | 1.0 | NEW |
| SMS-04 | /0/x/6 SMS security mode: 1 DTLS (device endpoint, PSK), 2 Secure Packet Structure (smartcard endpoint), 3 NoSec, 4 reserved, 204-255 proprietary. Device endpoint = DTLS per RFC 7925 App. A: 29 bytes overhead leave 107 bytes of payload (99 with a token). | E.1 res 6; T §5.3.2.1 | desc | 1.0 | NEW |
| SMS-05 | Smartcard endpoint: 3GPP 31.115 / ETSI 102 225 Command Packets for request and response. SPI = crypto checksum + ciphering. AES (SHOULD; CBC for ciphering, CMAC for integrity) or 3DES (outer CBC, 3 keys). Single DES MUST NOT be used. Counter processed only if higher. TAR MUST be `B2 02 03`. Class 2 SMS, TP-PID 111111. The incoming TP-OA MUST be reused as the outgoing TP-DA. Secured Data is BER-TLV with tag "TBD (e.g. 0x05)" (A-19). | T §5.3.2.1.2, §5.3.2.1.3 | MUST | 1.0 | NEW |
| SMS-06 | Client policy: SMS-only clients MAY use NoSec only for debugging, otherwise SMS Secured. UDP+SMS-trigger clients MUST secure UDP and MAY use any SMS mode. SMS from an MSISDN not in /0/x/9 MUST be silently ignored by the client. | T §5.3 | MUST (c) | 1.0 | NEW |
| SMS-07 | Trigger message example: WAP Push, WDP destination port 2948, X-WAP-Application-ID 0x9A (`x-wap-application:lwm2m.dm`), carrying a 14-byte CoAP POST /1/0/8 (informative). Vector `spec-coap-sms-trigger-execute-1-0-8`. | C App. L | desc | 1.0 | NEW |

### 1.16 HTTP binding (T §7, 1.2)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| HTTP-01 | Optional to implement and to use. If implemented: SHOULD use HTTP/1.1 (RFC 2616), MAY use HTTP/2. If an implementation claims conformance it MUST support every feature of T §7. Secured by TLS. The IR interface supports only Send. Read-Composite and Write-Composite are not supported. | T §7, §7.1.4 note | SHOULD/MUST | 1.2 | NEW |
| HTTP-02 | Alternate path as for CoAP: the Server MUST prepend it for DM/IR, and the BS MUST use `/{o}/{i}/{r}`. | T §7.1.1 | MUST | 1.2 | GEN-08 |
| HTTP-03 | Bootstrap (Tbl 7.1.2-1). Bootstrap-Request `POST /bs?ep=&pct=`. Bootstrap-Read `GET /{o}`, Accept TLV, LwM2M CBOR, SenML CBOR or SenML JSON, "Object ID MUST be '2'". Bootstrap-Write `PUT /{o}/{i}/{r}` {value}. Bootstrap-Delete `DELETE /{o}/{i}`. Bootstrap-Discover `GET /{o}`, Accept application/link-format. Bootstrap-Finish `POST /bs`. Bootstrap-Pack-Request `GET /bspack?ep=&acc=`, Accept SenML CBOR, SenML JSON or LwM2M CBOR. | T §7.1.2 | MUST | 1.2 | NEW |
| HTTP-04 | Bootstrap status codes (Tbl 7.3-1). Request 200 / 400 / 415. Read 200 or 204 / 400 / 401 or 403 / 404 / 400 or 405 / 406. Write 200 or 204 / 400 / 415. Discover 200 / 400 / 404. Delete 200 or 204 / 400. Finish 200 or 204 / 400 / 406. Pack 200 / 400 / 401 / 404 / 405 / 406 / 501. | T §7.3 | MUST | 1.2 | NEW |
| HTTP-05 | Registration (Tbl 7.1.3-1). `POST /rd?ep=&lt=&lwm2m=&b=&Q&sms=&pid=` with link-format payload; the Server MUST return a location under /rd. Update `POST /{location}?lt=&b=&Q&sms=`. De-register `DELETE /{location}`. A new security context ⇒ the client MUST register again. Retries are advised. Codes (Tbl 7.3-2): Register 201 / 400 / 401 or 403 / 409 / 412. Update 200 or 204 / 400 / 404. De-register 200 or 204 / 400 / 404. | T §7.1.3, §7.3 | MUST | 1.2 | NEW |
| HTTP-06 | DM (Tbl 7.1.4-1). Read `GET path` (Accept). Discover `GET path?depth=`. Replace `PUT /o/i[/r[/ri]]`. Partial Update `POST /o/i` or `/o/i/r` (multi-instance). Write-Attributes `PUT path?pmin=…&hqmax=` (the table also lists a {New Value} payload). Execute `POST /o/i/r`, none or text/plain. Create `POST /o`, SenML CBOR, SenML JSON, LwM2M CBOR or TLV. Delete `DELETE /o/i` or `/o/i/r/ri`. Write MUST carry the format. An unsupported format or Accept MUST be rejected. Inconsistent attributes MUST be rejected. A valueless attribute MUST unset. Execute plain-text arguments MUST follow the ABNF. | T §7.1.4 | MUST | 1.2 | NEW |
| HTTP-07 | DM status codes (Tbl 7.3-3), as printed. Create 200 or 204 / 400 / 401 or 403 / 404 / 405 / 400 / 415. Read 200 / 400 / 401 / 404 / 405 / "406 Bad Request". Write 200 or 204 / 400 / 401 or 403 / 404 / 405 / 406 / 413 / 415. Delete 200 or "204 Not Modified" / 400 / 401 or 403 / 404 / 405. Execute 200 / 400 / 401 or 403 / 404 / 405. Write-Attributes 200 or "204 Not Modified" / 400 / 401 or 403 / 404 / 405. Discover 200 / 400 / 401 or 403 / 404 / 405. No 4xx fits ⇒ the client MUST return 5xx. | T §7.3 | MUST | 1.2 | NEW |
| HTTP-08 | Send = `POST /dp`. The "Content-Format HTTP option (header)" MUST be set, and MUST be LwM2M CBOR, SenML JSON or SenML CBOR. Codes 200 or 204 / 400 / 404. No Observe or Notify. | T §7.1.5, §7.3 Tbl 7.3-4 | MUST | 1.2 | NEW |
| HTTP-09 | Queue mode: the Server MUST support it. The client signals it is awake with a registration update. | T §7.2 | MUST | 1.2 | QM-01 |
| HTTP-10 | Binding H: the Server MUST send its requests over HTTP. How it reaches the client (client-side HTTP server, address) is not specified (A-6). | C §6.2.1.2 | MUST | 1.2 | GEN-12 |

### 1.17 MQTT binding (T §8, 1.2)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| MQTT-01 | Works over MQTT 3.1.1 and MQTT 5 (no v5 features used). Exchanges are TLS-protected client↔broker. Server URI scheme MUST be `mqtts://` or `mqtt://`. Binding letter M; when M is current the Server MUST send requests over MQTT. | T §8, §8.6, §8.8; C §6.2.1.2 | MUST | 1.2 | NEW |
| MQTT-02 | Topic (ABNF): `topic = [ PREFIX "/" ] "lwm2m/" ( "bs" / "rd" ) "/" ENDPOINT`, where PREFIX and ENDPOINT are UTF8-octets. "bs" for the Bootstrap interface, "rd" for all other interfaces. PREFIX is configured per deployment (tenant, server ID) and may be omitted. ENDPOINT "has to match" the Register ep, or the security-protocol identifier when no ep is sent. | T §8.2 | MUST | 1.2 | NEW |
| MQTT-03 | Pub/sub. The Server subscribes `{PREFIX}/lwm2m/rd/#` and publishes requests and responses to `{PREFIX}/lwm2m/rd/{ENDPOINT}`. The client subscribes to `{PREFIX}/lwm2m/rd/{ENDPOINT}` and also publishes there. The BS uses the same scheme with `bs`. | T §8.3 | MUST | 1.2 | NEW |
| MQTT-04 | Every message is a CBOR map. Keys MUST follow Tbl 8.7-1: operation 1, token 2, ep 3, pct 4, uri 5, paths 6, payload 7, lifetime 8, version 9, b 10, sms 11, pmin 12, pmax 13, gt 14, st 15, epmin 16, epmax 17, result 18, ct 19, edge 20, hqmax 21, depth 22. There is no key for `lt` (less-than), `pid` or `con` (A-7). | T §8.3, §8.7 | MUST | 1.2 | NEW |
| MQTT-05 | token (uint) is chosen so that responses map unambiguously to requests; no randomness needed. Requests are tagged by `operation`, responses by `result`. | T §8.3, §8.4 | MUST | 1.2 | NEW |
| MQTT-06 | Bootstrap ops (`bs` topic). 0 Bootstrap-Request {token, ?pct}. 1 Bootstrap-Write {token, ?uri, ct (TLV, LwM2M CBOR, SenML CBOR or SenML JSON), payload}. 2 Bootstrap-Read {token, uri}. 3 Bootstrap-Delete {token, uri}. 4 Bootstrap-Discover {token, uri}. 5 Bootstrap-Finish {token}. 6 Bootstrap-Pack-Request {token, ?payload = BS-account instances}. Example `{1: 0, 2: 42}` to `tenant-a/lwm2m/bs/<ep>`. | T §8.3.1 Tbl 8.3.1-1 | MUST | 1.2 | NEW |
| MQTT-07 | Registration ops (`rd`). 6 Register {token, lifetime, version, ?b, ?sms, ?pid, ?payload = link-format bytes}. 7 Update {token, ?lifetime, ?b, ?sms, ?pid, ?payload}. 8 De-register {token}. Code 6 is shared with Bootstrap-Pack-Request; the topic (bs/rd) tells them apart. | T §8.3.2 Tbl 8.3.2-1 | MUST | 1.2 | NEW |
| MQTT-08 | DM ops. 9 Read {uri}. 10 Read-Composite {paths = SenML-ETCH CBOR bytes}. 11 Discover {uri, ?depth}. 12 Write-Replace {uri, ct, payload}. 13 Write-Partial-Update {uri, ct, payload}. 14 Write-Attributes {uri, ?pmin, ?pmax, ?gt, ?lt, ?st, ?epmin, ?epmax, ?edge, ?hqmax}. 15 Write-Composite {ct, payload}. 16 Execute {uri, ?payload = arguments}. 17 Create {uri, ct, payload}. 18 Delete {uri}. Every message also carries token. Example `{1: 9, 2: 56, 5: "/3/0/0"}`. | T §8.3.3 Tbl 8.3.3-1 | MUST | 1.2 | NEW |
| MQTT-09 | IR ops. 20 Observe {uri}. 21 Observe-Composite {paths}. 22 Cancel-Observe {token = the observation's token}. 23 Notify {token = the observation's token, ct, payload}. 24 Send {token, ct (LwM2M CBOR, SenML CBOR or SenML JSON), payload}. There are no attribute parameters on Observe and no separate Cancel-Composite. | T §8.3.4 Tbl 8.3.4-1 | MUST | 1.2 | NEW |
| MQTT-10 | Response = `{18: result, 2: token, ?19: ct, ?7: payload}`. Results are uint, "loosely based on CoAP": 201, 202, 204, 205, 400, 401, 403, 404, 405, 406, 408, 409, 412, 413, 415, 501 as per Tbls 8.5-1..4 (e.g. Bootstrap-Request 204; Read 205; Register 201; Delete and De-register 202; Bootstrap-Pack-Request 205/…/501). If none fits, the client MUST return a generic 500, 501 or 503 (Tbl 8.5-5). | T §8.4, §8.5 | MUST | 1.2 | NEW |
| MQTT-11 | Optional end-to-end protection: `Outer_Wrapper = {1 msg-wrapper: bstr .cbor [*(COSE_Encrypt / COSE_Encrypt0)] / nil, 2 lwm2m-message: <one of the payload maps>}`. If the /0 instance of the server validly links a /23 COSE instance (/0/x/27), the client MUST use COSE (RFC 8152) with that server. /23: 0 KID, 1 AEAD alg, 2 Key. | T §8.6, §8.8; E.10 | MUST | 1.2 | NEW |
| MQTT-12 | /24 MQTT Server object, linked from /0/x/26, used only when the URI scheme is MQTT: 0 Will Retain, 1 Will Topic, 2 Will Message, 3 Clean Session, 4 Will QoS (0..3), 5 Keep Alive (0..65535), 6 Client Identifier (mandatory; brokers must accept 1-23 bytes of `[0-9a-zA-Z]`), 7 User Name, 8 Password. | T §8.8; E.11 | desc | 1.2 | NEW |
| MQTT-13 | Recommended deployment security (lowercase): the broker forwards BS-bound messages only to the BS, isolates tenants, and isolates traffic of different Servers (ACLs, or a PREFIX per tenant or server). | T §8.1 | desc | 1.2 | NEW |

### 1.18 CBOR: LwM2M CBOR (11544) and plain CBOR (60)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| CBOR-01 | The Server MUST support LwM2M CBOR, `application/vnd.oma.lwm2m+cbor`, Content-Format **11544** (number in the spec since 1.2.1). Binary, no parameters. | C §7.5 Tbl 7.5-3, App. I.3; ERELD §5.2 | MUST | 1.2 (CF 1.2.1) | FMT-01 Δ |
| CBOR-02 | When this format is used, the payload MUST follow C §7.5.4. | C §7.5.4 | MUST | 1.2 | NEW |
| CBOR-03 | Grammar. `payload = CBOR_map_with_length 1*(ID, VALUE) / CBOR_infinite_map_start 1*(ID, VALUE) CBOR_break`. `ID = CBOR_unsigned_integer / CBOR_array_with_length 1*(CBOR_unsigned_integer) / CBOR_infinite_array_start 1*(CBOR_unsigned_integer) CBOR_break`. `VALUE = CBOR_value / payload`. The decoder MUST accept definite and indefinite maps and arrays, uint keys and array keys, and nesting at any depth. Maps are never empty (`1*`). | C §7.5.4 | MUST | 1.2 | NEW |
| CBOR-04 | IDs in the top-level map MUST start with the Object ID: paths are absolute from the object, whatever the request URI. | C §7.5.4 | MUST | 1.2 | NEW |
| CBOR-05 | A path is the concatenation of keys along the nesting, and inner keys are relative. `{[3,0,0]: v}` ≡ `{3:{0:{0: v}}}` (both valid, §7.5.4.1). Inside `{[2,5]: {…, [2,102]: 0}}` the inner key gives /2/5/2/102. One top-level map may mix depths (`{[3,0]: {…}, [1,0,1]: 86400}`). Recommended reading for duplicate paths: reject (A-8). | C §7.5.4.1-§7.5.4.6 | MUST | 1.2 | NEW |
| CBOR-06 | Leaf values (Tbl C-2, "CBOR, LwM2M CBOR and SenML CBOR" column). String = text string (RFC 8949 §3). Integer = major type 0/1. Unsigned = major type 0. Float = floating point (§3 and §3.3, any width). Boolean = true/false (Tbl 4). Opaque = byte string. Time = unsigned integer, or date/time per §3.4.1 (tag 0 string) / §3.4.2 (tag 1 epoch). Objlnk = text "oid:iid", decimal 16-bit. Corelnk = text string. none = null. | C App. C Tbl C-2 | MUST | 1.2 | DT-02 |
| CBOR-07 | A multi-instance resource is a map keyed by resource-instance ID (`{[3,0,6]: {0: 1, 1: 5}}`), or uses array keys down to /o/i/r/ri. | C §7.5.4.2 | MUST | 1.2 | NEW |
| CBOR-08 | Create: instance ID 65535 (MAX_ID) MAY mean "no instance reference" (`{[2,65535]: {…}}`). The client then MUST assign the ID. | C §7.5.4, §6.3.6 | MAY | 1.2.1 | NEW |
| CBOR-09 | Allowed uses. Read and Observe responses. Write (PUT/POST). Create. Write-Composite (iPATCH). Read-Composite and Observe-Composite responses (Accept), but not their request bodies. Notify. Send. Bootstrap-Write, the Bootstrap-Read response, Bootstrap-Pack and `pct`. | C §6.1.7.1, §6.1.7.5, §6.3.3, §6.3.6, §6.3.9, §6.4.6; T Tbls 6.4.2-1, 6.4.4-1, 6.4.5-1 | MUST | 1.2 | NEW |
| CBOR-10 | The client MUST support plain text, opaque and CoRE Link, and SHOULD support at least one of SenML CBOR, SenML JSON or LwM2M CBOR (the server must cope with any one of them). | C §7.5 | SHOULD (c) | 1.1 (LwM2M CBOR 1.2) | NEW |
| CBOR-11 | Gateway extension: `ID = PREFIX / uint / array [PREFIX] 1*uint / indef-array [PREFIX] 1*uint`, with `PREFIX = CBOR_string` (e.g. `{["d01",3,0]: {0:"Company A", 9:100}}`). | GW §9, §10 | MUST | GW 1.1 | NEW |
| CBOR-12 | Plain CBOR (60): only for Read and Write of a single resource or resource instance. All data types map to major types, with or without optional tags (Tbl C-2). | C §7.5.3 | MUST | 1.1.1 | FMT-01 |

### 1.19 ETCH: SenML-ETCH (RFC 8790)

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| ETCH-01 | Content-Formats: application/senml-etch+json **320**, application/senml-etch+cbor **322**. The Server MUST support both ("all data formats"). | C Tbl 7.5-3, §7.5; RFC 8790 §7.1 | MUST | 1.2 | FMT-01 Δ |
| ETCH-02 | A 1.2+ client that supports Read-Composite and/or Write-Composite MUST support 320 or 322. Nothing signals which one it supports except `ct` on the Register root link, so a server should be ready to use either. | C §7.5.6 | MUST (c) | 1.2 | NEW |
| ETCH-03 | Read-Composite and Observe-Composite request bodies (FETCH) use a Fetch Pack: SenML-ETCH JSON/CBOR, or plain SenML 110/112. Write-Composite (iPATCH) uses a Patch Pack: SenML-ETCH JSON/CBOR, SenML or LwM2M CBOR. MQTT Read-Composite `paths` = SenML-ETCH CBOR bytes. | C §6.3.8, §6.3.9; T Tbls 6.4.4-1, 6.4.5-1, §8.3.3 | MUST | 1.2 | DM-12 |
| ETCH-04 | Fetch Pack: ≥1 record. Each record MUST have `n` and/or `bn`, and MUST NOT contain fields other than n, bn, t, bt, u, bu; the receiver MUST reject a pack with other fields. Names resolve as bn+n. Without time or unit, a record matches all target records with that name. Each target record appears in the response at most once. No match → empty pack. | RFC 8790 §3, §3.1 | MUST | 1.2 | NEW |
| ETCH-05 | Patch Pack: each record MUST match at most one target and MUST contain a value (or sum) field. `"v": null` (CBOR label 2 = 0xF6) MUST NOT be added and removes the matched record; in LwM2M this deletes a resource instance, which only ETCH can do (C §7.5.6 note). Records are applied in order. An invalid pack MUST be rejected with no change (all-or-nothing). Unknown fields MUST NOT cause an error (§5). | RFC 8790 §3.2, §5; C §7.5.6 | MUST | 1.2 | NEW |
| ETCH-06 | Names in SenML-ETCH carry the alternate path (DM/IR) and the gateway prefix (GW §9). | T §6.4.1; GW §9 | MUST | 1.2 | GEN-08 |
| ETCH-07 | RFC 8790 points to 4.22 (Unprocessable Entity) and 4.09 for bad packs, but 4.22 is not in the LwM2M code tables. Use 4.00 (A-11). | RFC 8790 §3.1-3.2; T §6.7 | desc | 1.2 | NEW |

### 1.20 FMT / DT / ID / VER: data formats, types, identifiers, versioning

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| FMT-01 | The Server MUST support every data format, including TLV (mandatory for 1.0 clients): 0 text/plain, 40 link-format, 42 octet-stream, 11542 TLV, 11543 OMA JSON, 60 CBOR, 110 SenML JSON, 112 SenML CBOR, 11544 LwM2M CBOR, 320/322 SenML-ETCH. | C §7.5, Tbls 7.5-1..3 | MUST | 1.0-1.2 | =FMT-01 |
| FMT-02 | 1.1+ Servers MUST accept both 110 and 11543. 1.1+ clients MUST NOT use 11543, and SHOULD NOT use TLV or 11543 at all. | C §7.5, §7.5.6 | MUST | 1.1 | FMT-02 Δ |
| FMT-03 | A message with data MUST state its Content-Format. A Server request MAY carry Accept; if the client does not accept it the request is rejected (4.06); without Accept the client uses its own preferred format. | C §7.5; T §6.4.4 | MUST | 1.0 | =FMT-03 |
| FMT-04 | TLV type byte. Bits 7-6: 00 Object Instance, 01 Resource Instance, 10 Multiple Resource, 11 Resource. Bit 5: ID 8/16 bit. Bits 4-3: 00 means the length is in bits 2-0; 01/10/11 mean an 8/16/24-bit length field follows, and bits 2-0 MUST be ignored. Max size 16.7 MB; nesting ≤3 levels. The OI TLV MUST be used when the request has no instance ID (any count) and is optional otherwise. The MR TLV MUST be used for every multi-instance resource (0, 1 or n instances). Unknown resources may be skipped. | C §7.5.5 Tbl 7.5.5-1 | MUST | 1.0 | =FMT-04 |
| FMT-05 | SenML JSON (110) MUST follow RFC 8428 JSON plus the Objlnk extension, and MUST support bn, bt, n, t, v, vb, vlo, vd, vs (Tbl 7.5.6-1). One record per resource (instance); name = bn + n, a full path if there is no bn. `bn "/"` lets a response carry the Objlnk hierarchy, breadth-first, each instance at most once. `t` is relative to `bt`, needed only for historical data; a missing time = 0. `v` for Integer, Float and Time; `vb` Boolean; `vlo` Objlnk "oid:iid"; `vd` Opaque as base64url without padding (Tbl C-2); `vs` everything else. | C §7.5.6, Tbl C-2 | MUST | 1.1 | =FMT-05 |
| FMT-06 | Legacy pre-IANA numbers 1541-1543 (Zephyr). There is no OMA text for them. | Zephyr | — | 1.0 interop | =FMT-06 |
| FMT-07 | SenML CBOR (112) MUST use RFC 8428 §6 (integer labels), MAY carry a single value, and puts Objlnk under the text key "vlo". | C §7.5.7 | MUST | 1.1 | NEW |
| FMT-08 | Plain text (0) is for a single resource or resource instance (Tbl C-2). Integer and Unsigned: ASCII decimal. Float: ASCII decimal (the example `6.667e-11` → `"0.00000000006667"`). Boolean: "0" or "1". Opaque: Base64 (RFC 4648, padded: `AQIDBAU=`). Time: ASCII integer. Objlnk: "oid:iid". Corelnk: string. | C §7.5.1, Tbl C-2 | MUST | 1.0 | NEW |
| FMT-09 | Opaque (42): raw octets for a single resource (firmware etc.). | C §7.5.2 | MUST | 1.0 | NEW |
| FMT-10 | OMA JSON (11543) per TS 1.0: `{"bn":…, "e":[{"n":…, "sv"/"v"/…}]}`. Only accepted, never generated towards 1.1+ clients. | C §7.5.6.1 | MUST | 1.0 | FMT-02 |
| DT-01 | Types (Tbl C-1). String UTF-8 (length limits MAY be defined). Integer 8/16/32/64-bit signed (also used for enumerations). Unsigned 8-64-bit (bitmask use: bit n = 2^n). Float 32/64. Boolean (8-bit, 0/1). Opaque. Time (signed Unix seconds). Objlnk (null = MAX_ID:MAX_ID). Corelnk. none (executables only). | C App. C Tbl C-1 | MUST | 1.0 (Unsigned, Corelnk 1.1) | =DT-01 |
| DT-02 | Per-format encodings in Tbl C-2. TLV: integers and unsigned are 1/2/4/8 bytes big-endian (two's complement for signed); float 4 or 8 bytes IEEE 754; Boolean length MUST be 1; Time = Integer; Objlnk = 2×uint16 BE (4 bytes); Corelnk = string. The Server MUST type-check retrieved values, and the client MUST reject wrongly typed incoming values. | C App. C Tbl C-2; C §7.1 | MUST | 1.0 | =DT-02 |
| DT-03 | Objlnk targets: `oid:65535` (the object), an existing `oid:iid`, or `65535:65535` (null). A Corelnk naming an instance needs the same validity check. | C §6.3.3, Tbl C-2 | MUST | 1.0 | NEW |
| ID-01 | Object, instance, resource and resource-instance IDs are 16-bit; 65535 is reserved (MUST NOT be used). SSID 1..65534 (0 and 65535 MUST NOT). | C §7.4 Tbl 7.4-1 | MUST | 1.0 | =ID-01 |
| ID-02 | `ep` MUST be unique on the server(s) it is used with. URI or URN RECOMMENDED. Current list (1.2.1): `urn:uuid:…`, `urn:dev:ops:<PEN>-<ProductClass>-<Serial>`, `urn:dev:os:<PEN>-<Serial>` (RFC 9039), `urn:gsma:imei:<TAC>-<SNR>-<Spare>` (RFC 7254), `urn:3gpp2:meid:<MfrCode>-<Serial>` (RFC 8464). Deprecated: the TR-069 OUI `urn:dev:ops`/`os` forms, `urn:imei:`, `urn:esn:`, `urn:meid:`, `urn:imei-msisdn:`, `urn:imei-imsi:`, `urn:imei-iccid:`, and `urn:extid:` (NAI ≤63 octets). `ep` MUST NOT be used alone for decisions. Watch for SQL injection. | C §7.4.1, Tbls 7.4.1-1/-2 | MUST | 1.0 (list 1.2.1) | ID-02 Δ |
| ID-03 | Object ID classes: 0-1023 oma; 1024-2047 reserved; 2048-10240 ext (SDO); 10241-32768 x (vendor); 32769-42768 company-reserved (x); 42769-42800 test (x, MUST NOT be used in production); 42801-65534 reserved. | C App. D.2.1 Tbl D.2.1-1 | MUST | 1.1 | NEW |
| VER-01 | The Server MUST be able to determine each object's specification unambiguously. The client MUST send `ver` when a core object's version differs from its enabler release, or a non-core object is not 1.0, in Register, Update and Discover. `ver` goes on the object link when there are 0 or ≥2 instances; with exactly 1 instance it MAY go on the instance link (the object link omitted). | C §7.2.1, §7.2.3 | MUST | 1.1 | =VER-01 |
| VER-02 | URN `urn:oma:lwm2m:{oma,ext,x}:ObjectID[:major.minor]`; 1.0 may be omitted. Type I (breaking) changes bump major, type II minor; all other changes are forbidden. | C §7.2.1, §7.2.2 | MUST | 1.1 | =VER-02 |
| VER-03 | Object versions of the 1.2.2 enabler (Tbl E-1): /0 1.2, /1 1.2, /2 1.1, /3 1.2, /4 1.3, /5 "1.1" (but E.6 defines /5 **1.2**, A-15), /6 1.0, /7 1.0, /21 2.0, /23 1.0, /24 1.0, /25 1.0, /26 1.0, /27 1.0. These are the versions a 1.2 client implies by omitting `ver`. | C App. E Tbl E-1 | MUST | 1.2 (/5 1.2 1.2.2) | NEW |
| VER-04 | Object XML MUST validate against LWM2M.xsd or LWM2M-v1_1.xsd. LWM2MVersion = minimum enabler version; ObjectVersion. | C App. D, App. J | MUST | 1.1 | NEW |

### 1.21 FW / OBJ: firmware update and other object duties

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| FW-01 | The Server MUST support block-wise transfer. A client implementing /5 MUST as well. | C E.6; T §6.1 | MUST | 1.0 | =FW-01 |
| FW-02 | Push: Write /5/0/0 with Block1 (example: POST, 128-byte blocks, 2.31 Continue, final 2.04). Pull: Write /5/0/1, then the client fetches (example GET with Block2). The repository may be any block-wise CoAP server. CoAP is RECOMMENDED for constrained devices; HTTP(S) MAY be used. The Security object, or OSCORE, protects the repository link. | C E.6, E.6.2; T §5.2.4 #3, §5.4.3 #3 | MAY/SHOULD | 1.0 | =FW-02 |
| FW-03 | The Server MUST NOT put a URI in /5/0/1 whose protocol the client does not list in /5/0/8: 0 CoAP+blockwise (default), 1 CoAPS, 2 HTTP 1.1, 3 HTTPS 1.1, 4 CoAP/TCP, 5 CoAP/TLS. The Server MUST ignore unknown values. An unsupported scheme makes the client set Update Result 9. | C E.6 res 1, 5, 8 | MUST | 1.1 | =FW-03 |
| FW-04 | States: 0 Idle, 1 Downloading, 2 Downloaded, 3 Updating. Update Result: 0 initial, reset to 0 when a download or update starts; 1 success; 2 flash; 3 RAM; 4 connection lost; 5 integrity; 6 package type; 7 URI; 8 failed; 9 protocol; 10 cancelled; 11 deferred. Errors MUST be reported only through Update Result. An empty URI or a NUL Package resets to Idle. Update is executable only in Downloaded. Undefined operations SHOULD be rejected. | C E.6 res 3, 5, E.6.1 | MUST (c) | 1.0 (10, 11: 1.1) | =FW-04 |
| FW-05 | After an update, instances of objects no longer supported MUST be deleted by the client (re-registration follows). | C E.6.3 | MUST (c) | 1.1 | =FW-05 |
| FW-06 | /5 v1.2 adds Cancel (10; 4.05 if installing or installed; on success Update Result 10 and State 0), Severity (11; 0 critical, 1 mandatory (default), 2 optional), Last State Change Time (12), Maximum Defer Period (13; 0 = no defer), Automatic Upgrade at Download (14; false ⇒ the Server must Execute /5/0/2). | C E.6 | MUST (c) | 1.1 obj / 14 in TS 1.2.2 | =FW-06 |
| FW-07 | /5/0/9 Delivery Method (mandatory): 0 pull, 1 push, 2 both (the Server MAY choose). | C E.6 res 9 | MAY | 1.1 | NEW |
| OBJ-01 | The Server MUST support the Security, Server and Device objects, and SHOULD support Access Control, Connectivity Monitoring, Firmware Update, Location and Connectivity Statistics. | C App. E | MUST/SHOULD | 1.1 | NEW (≈SCR OBJ-*) |
| OBJ-02 | Template rules. A Mandatory + Single object has exactly 1 instance. A Mandatory + Single resource has exactly 1. Executable resources MUST be Single with type none. Empty Operations = Bootstrap-only access. Range "a..b" is inclusive; for opaque and string a range means a length in octets; a quoted list is a string enumeration. | C App. D.1 | MUST | 1.0 | NEW |
| OBJ-03 | Device: /3/0/6-8 MUST share instance IDs per power source. /3/0/11 Error Code instance 0 = 0 means no error (MAY be observed). /3/0/5 Factory Reset MAY De-register first. /3/0/13 Current Time is writable by the Server for clock sync. | C E.4 | MUST (c) | 1.0 | NEW |

### 1.22 GW: LwM2M Gateway (/25, /26), from the separate Gateway TS 1.1.1

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| GW-01 | Two object definitions exist. Core 1.2.2 E.12/E.13: /25 v1.0 (0 Device ID R, 1 Prefix **RW**, 2 Routing Table Entry Objlnk→/26) and /26 v1.0 (0 Object ID, 1 Mapping Info Corelnk, where empty = identity). Gateway TS 1.1.1: /25 **v2.0** (LWM2M 1.1: 0 Device ID R, 1 Prefix R "MUST be used", 3 IoT Device Objects R Corelnk; no res 2; /26 not used). The Server must resolve the right one from `ver`. | C E.12, E.13; GW §6 | MUST | 1.2 / GW 1.1 | NEW |
| GW-02 | One /25 instance per IoT device. The gateway SHOULD create it on discovery and SHOULD delete it (with the Device Objects) when the device is no longer manageable, and sends an Update (desc) on create or delete. Device ID MUST let the Server uniquely identify the device and SHOULD be gateway-independent (URN RECOMMENDED). Prefix MUST be locally unique. | GW §6, §8.2 | MUST/SHOULD | GW 1.1 | NEW |
| GW-03 | Device Objects MUST NOT appear in Register or Update. The Server learns them by reading /25/x (res 3) and can observe them for changes. | GW §6, §8.2 | MUST | GW 1.1 | NEW |
| GW-04 | Bootstrap: Bootstrap Information (including a Pack) MUST NOT contain Device Objects. Bootstrap-Discover, -Read, -Write and -Delete MUST NOT target them. A Bootstrap-Delete or Bootstrap-Discover without an Object ID MUST NOT affect or report them. | GW §8.1 | MUST | GW 1.1 | NEW |
| GW-05 | Read, Discover, Write, Write-Attributes, Execute, Create, Delete and Observe take an optional Prefix. Without it the Object ID MUST be a gateway object; with it, an object of that device. An unknown prefix → "not found". Mapping: the Prefix MUST be treated as an Alternate Path (`GET /d01/3303/0`). With a real alternate path, the Server MUST append the prefix after it (`/lwm2m/d01/…`). | GW §8.3.1, §10 | MUST | GW 1.1 | NEW |
| GW-06 | A Discover with a prefix MUST NOT include the prefix in the returned links. | GW §8.3.1 | MUST (c) | GW 1.1 | NEW |
| GW-07 | Read-, Write- and Observe-Composite address device objects by prefixing names, and MAY mix several devices and the gateway in one request. | GW §8.3.2 | MAY | GW 1.1 | NEW |
| GW-08 | In LwM2M CBOR, SenML JSON/CBOR and SenML-ETCH the prefix MUST be part of names (CBOR-11 grammar). | GW §9 | MUST | GW 1.1 | CBOR-11 |
| GW-09 | Access Control does not apply to Device Objects: Servers have full rights. There is no end-to-end security to the device. The gateway MUST support LwM2M ≥1.1. | GW §7, §8 | MUST | GW 1.1 | NEW |

### 1.23 LORA / CIOT

| ID | Requirement | § | Level | New-in | Maps-to |
|---|---|---|---|---|---|
| LORA-01 | The LoRaWAN endpoint MUST be the client. Server and BS MUST be LoRaWAN Application Servers. NoSec mode (LoRaWAN provides security). Server URI MUST be `lorawan://{port}`, FPort 1-255. | T §6.8.4 | MUST | 1.1 | NEW |
| LORA-02 | Register defaults: ep = DevEUI (omitted if equal); lt 2 592 000 s (30 days); lwm2m optional, omitted when 1.1, values 1.1 or 1.2; LoRaWAN is inherently queue mode. The object list MAY be omitted on the first attempt. If the Server cannot get it out-of-band it MUST answer 4.09, and the client SHOULD retry with a list (which MUST be included). | T §6.8.4 Tbl 6.8.4-1 | MUST | 1.1 | NEW |
| LORA-03 | Piggybacked responses MUST be used. CoAP retransmission is disabled and replaced by App. C: MAX_RETRANSMIT 4, ACK_TIMEOUT 300 s. The Application Server MUST reply to CON as soon as possible (wording 1.2.2). Downlink CON in unconfirmed LoRaWAN: after the Network Server's sent-notification wait ACK_TIMEOUT, then resend, up to MAX_RETRANSMIT times. The endpoint MUST reply within ACK_TIMEOUT. The endpoint sends CoAP Empty messages to open RX windows. | T §6.8.4, App. C | MUST | 1.1 (1.2.2) | NEW |
| CIOT-01 | CIoT binding (N), informative App. D. NIDD via SGi needs a pre-configured destination, and via SCEF the server addresses the UE by MSISDN or External Identifier; either way one server per UE unless RDS (/0/x/28-30 ports and App ID) is used. No NAS segmentation: the server must not exceed the downlink limit (Non-IP MTU 1358). Avoid double buffering (queue mode plus network extended buffering). | T §6.8.5, App. D; E.1 res 28-30 | desc | 1.1 / 1.2 | NEW |

---

## 2. Static Conformance Requirements

**1.2.2 has no SCR tables.** Both C App. B and T App. B read exactly "This appendix is voided." So did 1.2.0 and 1.2.1 (checked in C120, T120, C121 and T121). There are no 1.2.x Server or Bootstrap-Server SCR item IDs to copy. The last published SCRs are 1.1.1. They are reproduced below **verbatim from C111 App. B.2/B.3 and T111 App. B.2** (item ID, function, M/O) for traceability. They are not 1.2.2 requirements, and §1 above (spec text) takes precedence.

### 2.1 Core 1.1.1, B.2 SCR for LwM2M Server

| Item | Function | Ref (1.1.1) | Status |
|---|---|---|---|
| LwM2M-ATTR-001-S-M | Support of Attributes | 5.1 | M |
| LwM2M-ATTR-002-S-M | Support of attachment of Attributes | 5.1.1 | M |
| LwM2M-ATTR-003-S—M | Support of characteristics of Attributes in Table 5.1.1. Attributes Definitions and Rules.-1 | 5.1.1 | M |
| LwM2M-ATTR-004-S-M | Support of <PROPERTIES> Class Attributes | 5.1.2 | M |
| LwM2M-ATTR-005-S-M | Support of <NOTIFICATION> Class Attributes | 5.1.2 | M |
| LwM2M-INTR-001-S-M | Support of relationships as indicated by Table 6.-1 | 6 | M |
| LwM2M-BOOT-005-S-M | Support of Server Initiated Bootstrap | 6.1.3.4 | M |
| LwM2M-BOOT-010-S-M | Support of Bootstrap Security | 6.1.5 | M |
| LwM2M-CR-001-S-M | Support of "Register" operation | 6.2.1 | M |
| LwM2M-CR-002-S-M | Support of Endpoint Client Name parameter | 6.2.1 | M |
| LwM2M-CR-003-S-M | Support of Lifetime parameter | 6.2.1 | M |
| LwM2M-CR-004-S-M | Support of LwM2M Version parameter | 6.2.1 | M |
| LwM2M-CR-005-S-M | Support of Binding Mode parameter | 6.2.1, 6.2.1.1 | M |
| LwM2M-CR-006-S-M | Support of SMS Number parameter | 6.2.1 | M |
| LwM2M-CR-007-S-M | Support of Object and Object Instances parameter | 6.2.1 | M |
| LwM2M-CR-008-S-M | Support of "Update" operation | 6.2.2 | M |
| LwM2M-CR-009-S-M | Support of "De-register" operation | 6.2.3 | M |
| LwM2M-CR-010-S-M | Support removal of the Client and existing observations, when no update is received | 6 | M |
| LwM2M-CR-011-S-O | Server to support Registration control resources | 6.2.1.1 | O |
| LwM2M-DMSE-001-S-M | Support of "Read" operation | 6.3.1 | M |
| LwM2M-DMSE-002-S-M | Support of "Discover" operation | 6.3.2 | M |
| LwM2M-DMSE-003-S-M | Support of "Write" operation | 6.3.3 | M |
| LwM2M-DMSE-004-S-M | Support of "Write-Attributes" operation | 6.3.4 | M |
| LwM2M-DMSE-005-S-M | Support of Minimum Period parameter | 6.3.4 | M |
| LwM2M-DMSE-006-S-M | Support of Maximum Period parameter | 6.3.4 | M |
| LwM2M-DMSE-007-S-M | Support of Greater Than parameter | 6.3.4 | M |
| LwM2M-DMSE-008-S-M | Support of Less Than parameter | 6.3.4 | M |
| LwM2M-DMSE-009-S-M | Support of Step parameter | 6.3.4 | M |
| LwM2M-DMSE-010-S-M | Support of "Execute" operation | 6.3.5 | M |
| LwM2M-DMSE-011-S-M | Support of "Create" operation | 6.3.6 | M |
| LwM2M-DMSE-012-S-M | Support of "Delete" operation | 6.3.7 | M |
| LwM2M-DMSE-013-S-O | Support of "Read-Composite" | 6.3.8 | O |
| LwM2M-DMSE-014-S-O | Support of "Write-Composite" | 6.3.9 | O |
| LwM2M-DMSE-015-S-O | Support of "Send" | 6.4.6 | O |
| LwM2M-IR-001-S-M | Support of "Observe" operation | 6.4.1 | M |
| LwM2M-IR-002-S-M | Support of "Notify" operation | 6.4.2 | M |
| LwM2M-IR-003-S-M | Support of "Cancel Observation" operation | 6.4.3 | M |
| LwM2M-IR-004-S-O | Support of "Observe-Composite" | 6.4.4 | O |
| LwM2M-IR-005-S-O | Support of "Cancel Observation-Composite" | 6.4.5 | O |
| LwM2M-IDT-001-S-M | Support of Plain Text format | 7.4.1 | M |
| LwM2M-IDT-002-S-M | Support of Opaque format | 7.4.2 | M |
| LwM2M-IDT-003-S-M | Support of TLV format | 7.4.4 | M |
| LwM2M-IDT-004-S-M | Support of JSON format | 7.4.5.1 | M |
| LwM2M-IDT-005-S-M | Support of CBOR format | 7.4.3 | M |
| LwM2M-IDT-006-S-M | Support of Data Types in Appendix C | App. C | M |
| LwM2M-IDT-007-S-M | Support of a unique Client Identifier | 7.3 | M |
| LwM2M-IDT-008-S-M | Support of new media types SenML JSON, SenML CBOR | 7.4 | M |
| LwM2M-MEC-001-S-M | Support of Queue Mode | 6.2.1.2 | M |
| LwM2M-MEC-002-S-M | Support of UDP Binding | 6.2.1.2 | M |
| LwM2M-MEC-003-S-O | Support of SMS Binding | 6.2.1.2 | O |
| LwM2M-OBJ-001-S-M | Support of LwM2M Security Object | E.1 | M |
| LwM2M-OBJ-002-S-M | Support of LwM2M Server Object | E.2 | M |
| LwM2M-OBJ-003-S-O | Support of Access Control Object | E.3 | O |
| LwM2M-OBJ-004-S-M | Support of Device Object | E.4 | M |
| LwM2M-OBJ-005-S-O | Support of Connectivity Monitoring Object | E.5 | O |
| LwM2M-OBJ-006-S-O | Support of Firmware Update Object | E.6 | O |
| LwM2M-OBJ-007-S-O | Support of Location Object | E.7 | O |
| LwM2M-OBJ-008-S-O | Support of Connectivity Statistics Object | E.8 | O |
| LwM2M-OBJ-009-S-O | Support of OSCORE object | E.9 | O |

The "S—M" on ATTR-003 is as printed (an em dash). Note that 1.1.1 numbered the Server DMSE items differently from the client ones (client DMSE-010 = Cancel parameter).

### 2.2 Core 1.1.1, B.3 SCR for LwM2M Bootstrap Server

| Item | Function | Ref (1.1.1) | Status |
|---|---|---|---|
| LwM2M-BOOT-001-BS-M | Support "Client Initiated Bootstrap" | 6 | M |

That is the whole table. T111 has no Bootstrap-Server SCR.

### 2.3 Transport 1.1.1, B.2 SCR for LwM2M Server

| Item | Function | Ref (1.1.1) | Status |
|---|---|---|---|
| LwM2M-SEC-002-S-M | Support of Pre-Shared Keys mode | 5.2.8.1 | M |
| LwM2M-SEC-003-S-M | Support of Raw Public Key Certificates mode | 5.2.8.2 | M |
| LwM2M-SEC-004-S-M | Support of X.509 Certificates mode | 5.2.8.3 | M |
| LwM2M-SEC-005-S-M | Support of No Sec mode | 5.3 | M |
| LwM2M-SEC-006-S-M | Support of UDP Channel Security | 5.2 | M |

Equivalents in the 1.2.2 text:

- ATTR-*: ATT-01 (but PROPERTIES is now SHOULD).
- CR-*: REG-01..22.
- DMSE-013/014 (O): DM-01 reads MUST now (A-3).
- DMSE-015 Send: still MAY (SEND-01).
- IR-004/005 (O): same reasoning as A-3. C §6.4 says the Server MUST support all IR operations unless stated, and only the client is given MAY for Observe-Composite. So the composites are MUST.
- MEC-003 SMS: no longer SHOULD.
- SEC-002..006: conditional (A-4).

---

## 3. Worked examples → `spec/vectors/spec-examples.json`

106 vectors, valid JSON, in the README schema; `source` cites document and section. They are copied from the HTML rendering of C and T (multi-line JSON as rendered, lines trimmed) and from the PDF of GW and the figures. A self-check was run, and the script is not in the repo: it decodes every CBOR vector, re-parses the TLV vectors that are not errata and resolves the SenML names, then checks each against `expected`.

| Content-Format | n | Covers |
|---|---:|---|
| 11544 LwM2M CBOR | 9 | §7.5.4.1 (both key styles), .2, .3, .4, .5, .6 (instance 5 and 65535), GW §10 prefix |
| 11542 TLV | 6 | §7.5.5.1, .2 A/B/C, .3 ex 1/2. **4 of 6 are internally inconsistent** in the spec (`expected:"error"`, see A-13) |
| 112 SenML CBOR | 1 | §7.5.7 = App. M (verified byte-identical, 196 B) |
| 110 SenML JSON | 20 | Tbls 7.5.6-2…-12, Bootstrap-Read /2, Bootstrap-Pack (C and T), alternate path, Write /34/0/1, Send ×2, GW read and composite request (erratum) |
| 11543 OMA JSON | 1 | §7.5.6.1 |
| 0 plain text | 13 | §7.5.1, Tbl C-2 rows, Execute-argument examples (§6.3.5) |
| 40 link-format | 36 | Bootstrap-Discover ×5 + legacy, `acc` ×2, Register/Update/version/alternate-path/queue/GW payloads, Discover ×8, Observe-Composite attributes, two errata |
| null (other) | 20 | Write-Attributes queries ×7, Profile ID, MQTT ×4, SMS trigger CoAP, Register/Bootstrap URIs ×7 |

Six vectors are must-fail (`"error"`): four TLV errata, a link payload missing a comma, and the GW composite `"/9"` name. The README has no shapes for some of these, so they use these extra `expected` objects:

- `{"execute_args":[{"digit":n,"value"?:s}]}`
- `{"profile_ids":[{"kind","suite","hash_hex"}]}`
- `{"mqtt":{"topic", "message":{cbor-key: value}}}` (bytes as hex)
- `{"coap":{type,code,message_id,token,uri_path,payload_hex}}`
- `{"request":{method,path[],query{}}}` (a valueless flag maps to `null`)

**Not invented.** These have no source bytes, so they are reproduced only as diagnostic notation or text:

- MQTT `bytes_hex` is our own deterministic encoding of the spec's diagnostic notation; the notes say so.
- The MQTT Register example's payload is elided in the spec (`h'a8393..8aa8a'`) and is skipped.
- The MQTT Send example's malformed hex is replaced by the encoding of its own diagnostic notation; the notes say so.
- There are no SenML-ETCH or Profile-ID hash examples with real values in 1.2.2. The RFC 8790 examples use non-LwM2M names and were not copied.

---

## 4. Ambiguities and contradictions (recommended reading)

| # | Where | Problem | Recommended reading |
|---|---|---|---|
| A-1 | C §6.1.7.4 vs T Tbl 6.4.2-1 vs T Tbl 7.1.2-1 | Bootstrap-Read targets. Core and the CoAP table (fixed in 1.2.0) say /1 or /2. The HTTP table still says "MUST be '2'". | Accept and send /1 and /2 on all bindings. |
| A-2 | C §6.1.7.6 / §6.1.7.6-1 / §6.1.7.7 | BS-account exceptions. Bootstrap-Delete prose protects the /21, /23 and /24 instances of the BS account. Its parameter table and the Bootstrap-Pack text mention only /21. | Protect all of the BS account's /0, /21, /23 and /24 instances in every delete or replace. |
| A-3 | C §6.3, §6.4 vs 1.1.1 SCR | Server support of Read-, Write- and Observe-Composite. The text says Server MUST support all operations unless stated; only the client gets MAY. 1.1.1 SCR said O, and 1.2.x has no SCR. | Implement them (needed for "most compliant"), but never require client support. |
| A-4 | T §5.2.9.1-3 vs T111 SCR | Server security modes are conditional ("If a Server supports…"). No text makes PSK, RPK or X.509 mandatory for a 1.2.2 Server, but the BS MUST support all three. | Support all three, plus NoSec for lower-layer-secured deployments. |
| A-5 | T §6.4.4, §6.8.1 | GEN-03's CON rule sits under the UDP binding. RFC 8323 (TCP) has no CON/NON. | It applies to UDP and SMS framing only. |
| A-6 | T §7, C §6.2.1.2 | HTTP binding "H": the Server MUST send requests over HTTP, but nothing says how it reaches the client (does the client run an HTTP server, at which address?). HTTP "Content-Format option (header)" is not a real header. HTTP/2 is cited as [RFC8132] (should be RFC 7540). Create returns 200/204 but no instance-ID carrier is defined. Tables print "406 Bad Request" and "204 Not Modified". The Write-Attributes row lists a {New Value} payload. Composite codes are listed although composites are unsupported. The alternate-path example has spaces (`</lwm2m /1/0>`). | Use `Content-Type` and `Accept` with the IANA media types (RFC 8075 mapping), and `Location` for 201/Create. Treat 204 as No Content and send no Write-Attributes body. Treat the HTTP binding as experimental. |
| A-7 | T §8.3.3, §8.7 | MQTT CBOR key table has no key for `lt` (less-than), `pid` or `con`, although the CDDL uses `lt` and `pid`. `ep` (key 3) appears in no CDDL. The Notify/Send example uses operation 23 (Notify) and labels it Send (Send is 24). The Send payload hex has an odd digit count. The Generic Response example puts a text string in `payload`, typed `bstr`. Outer_Wrapper refers to `Msg_AuthEnc_Wrapper` but defines `Msg_Wrapper`. The topic note says wildcards are `#`, `*`, `$` (MQTT uses `#` and `+`). Write-Composite lists TLV as a format. Requests and responses share one topic, so publishers receive their own messages. There is no queue-mode text for MQTT. | Distinguish requests and responses by key 1 vs key 18 and drop your own echoes by token. Do not invent keys for lt, pid or con: reject or ignore them until OMA assigns them, and document the gap. Accept the payload as bstr or tstr. Send = 24. |
| A-8 | C §7.5.4 | LwM2M CBOR does not define: duplicate paths (`{3:{0:{0:x}}, [3,0,0]:y}`); signed or negative Time, although the Time type is signed but CBOR Time is "unsigned integer or date/time string"; null values outside Create; deletion in Write-Composite; the alternate path (only SenML is mentioned). | Reject duplicates (4.00). Accept negative ints as Time. Treat null as "none" only for executables. Never use the alternate path in LwM2M CBOR keys (keys are numeric IDs). |
| A-9 | C Tbl 6.2.2-1 vs T Tbl 6.4.3-2 / 7.1.3-1 | The Update URI templates omit `pid`, though Profile ID is an Update parameter (and MQTT Update has `pid`). The templates include `Q`, which Tbl 6.2.2-1 does not list. | Accept `pid` and `Q` on Update. |
| A-10 | C §6.2.1 | The dynamic Profile-ID fingerprint has no canonical input (serialization, ordering, instance format). Server and client cannot compute matching hashes independently. | Treat dynamic pids as opaque cache keys learned from a full registration (PROF-11). On a cache miss answer 4.09. |
| A-11 | RFC 8790 vs T §6.7 | ETCH error code 4.22 is not a valid LwM2M code. RFC 8790 also says every Patch Record MUST carry a "Value" field, and LwM2M's `vlo` is not an RFC 8428 value field. | Answer 4.00. Accept `vlo` as a value in Patch Packs. |
| A-12 | T §6.6.1 vs C §6.3.5 | The binding override on /1/x/8 is shown as a URI query (`POST /1/x/8?0=U`), while Execute arguments are a text/plain payload everywhere else. | Send `0='U'` as the payload. Leniently accept the query form if acting as a client simulator. |
| A-13 | C §7.5.5 | TLV examples. Device /3/0 and /3 spell "Lightweigt" (21 bytes vs length 22), so the stated 121/124 bytes are really 120/123. /1 has instance length 0x0D for 15 bytes and the text and table disagree (18 vs 16). /66 has instance lengths 0x23 for 38 bytes (stated 76, actually 82). The /2 table says "0x0E (17 bytes)". The /65 TLV gives Res 2 = 0x12345678, but the SenML version gives 1 and "myService1" vs "myService 1". | Vectors keep the bytes and expect a decode error. Implement TLV from Tbl 7.5.5-1, not from the examples. |
| A-14 | T §6.5 vs App. D.8.3 | Queue-mode awake time: MAX_TRANSMIT_WAIT (RECOMMENDED) vs "MUST wait at least ACK_TIMEOUT" (an informative appendix). | Size the server's awake window to MAX_TRANSMIT_WAIT (93 s) by default, configurable. |
| A-15 | C Tbl E-1 vs E.6 | /5 is listed as v1.1 but defined as v1.2 (urn …:5:1.2, 15 resources). | A 1.2 client omitting `ver` for /5 means 1.1 per Tbl E-1 (the Tbl E-1/§7.2.3 rule). Tolerate 1.2 resources (13, 14) as unknown optional ones. |
| A-16 | E.1 res 23/24 vs T §2.1 | CID bit 12 cites the pre-RFC drafts while the TS cites RFC 9146. The resources are 0..65535 "Unsigned" yet speak of bits up to 31. | Treat bit 12 as RFC 9146 CID. Support the RFC codepoint and, if needed for Zephyr/mbedTLS, the legacy draft codepoint. |
| A-17 | C §6.2.1 | Register excludes /0, /21 and /23 but not /24 MQTT Server, which, like /23, can belong to the BS account. | Ignore /24 instances in a Register list (never expose them to DM). |
| A-18 | T §5.4.7.1 vs E.9 | OSCORE Sender and Recipient IDs are stored "as an UTF-8 string" in resources typed Opaque. The RFC 8613 IDs are byte strings. | Treat them as opaque bytes. The UTF-8 remark matters only for the ep = Sender ID comparison (OSC-05): compare bytes. |
| A-19 | T §5.3.2.1.3 | The SMS Secured Data BER-TLV tag is "TBD : e.g. 0x05". | Use 0x05, configurable. |
| A-20 | C §6.1.7.3 examples | Bootstrap-Discover examples use trailing slashes (`</0/1/>`, `</1/0/>`, `</21/1/>`), SP after commas (not allowed by RFC 6690 ABNF), and in one example ssid/uri on the wrong instance. The Note admits earlier examples broke the ABNF. | The link parser skips optional whitespace and strips one trailing slash. |
| A-21 | C §6.3.2 examples | The "Discover /3/0" example lists resource instances (depth 3) although the default instance-level depth is 1. §7.1's "DISCOVER /1" lists only resource links. The /3 prose swaps pmin and pmax. | Follow Tbl 6.3.2-2. Parse whatever depth comes back. |
| A-22 | C Tbl 7.3.2-1 | gt, lt and st syntax `1*DIGIT ["." 1*DIGIT]` allows neither a sign nor an exponent, so negative thresholds (e.g. RSSI) cannot be written. | Accept an optional leading "-" and exponents on input. Emit plain decimal. |
| A-23 | C §6.3.5 | The "valid" Execute example `7,0=' <https://www.omaspecworks.org>'` contains SP, which CHAR excludes (the "unauthorized spaces" fix in 1.2.1 was incomplete). | The Server never emits SP inside arguments. A parser may accept it. |
| A-24 | C Tbl 7.5.6-1 vs Tbl C-2; §7.5.6-4 | SenML `vd` is "Base64" in one table and "URL-safe, padding omitted" in the other. The null Objlnk is written `"FFFF:FFFF"` (hex) vs "two 16-bit ASCII integers". SenML `v` lists Integer, Float and Time but not Unsigned. | Emit base64url without padding and decimal `65535:65535`. Accept both base64 alphabets with or without padding, and hex FFFF. Put Unsigned in `v`, parsing integers exactly (no float64). |
| A-25 | GW §10; GW Tbl 10.-1; C E.12 vs GW §6 | The gateway composite example `{"bn":"/d01/3/0/","n":"0"},{"n":"/9"}` resolves to `/d01/3/0//9`. `</5/0>;ver1.1` is missing "=". /25 Prefix is RW in Core v1.0 and R in GW v2.0. /26 has no defined semantics in the GW TS. | Read the composite example as /d01/3/0/9. Pick the /25 model by `ver`. Never write /25/x/1. |
| A-26 | C §6.2 vs T §6.4.3 Fig 6.4.3-1 | The figure payload `</2/0></2/1>` has no comma. | Strict parsers reject it. It is a figure typo, not a format variant. |
| A-27 | T §5.2.9.2/3 | The RPK section cites RFC 6655 for ECDHE_ECDSA_CCM_8 (it is RFC 7251), and says clients "SHOULD NOT use TLS_PSK_WITH_AES_128_CBC_SHA256" (copied from the PSK section). | Read it as ECDHE_ECDSA_…_CBC_SHA256 being discouraged for clients. The Server still MUST support it. |
| A-28 | C §6.3.2 (1.2.2) | 1.2.2 dropped "optional resources in the Discover response … MUST NOT be interpreted as an error by the Server". | Keep tolerating them (GEN-02 still covers this). |
| A-29 | C App. E / T §6.8 | Server SMS support was SHOULD (1.1) and the SCR made it O. 1.2.2 says nothing about it. | Optional. |
| A-30 | C §7.3.1 Tbl 7.3.1-1 vs 1.1.1 SCR ATTR-004-S-M | <PROPERTIES> support was M for servers via SCR and is now SHOULD. `ver` is "YES (Server)" in the same table. | Parse all of them. `ver` is effectively mandatory (VER-01). |

Carried over from `standards.md` and confirmed in 1.2.2: Bootstrap-Read /1 vs /2 (A-1, now only the HTTP table disagrees).

---

## 5. Not fetched or not verified

- The ERELD "link for the deltas between v1.2.2 and v1.2.1" points outside the release directory and was not followed. A text diff of C121/T121 against C/T was used instead (§0).
- draft-ietf-core-conditional-attributes (normative for ATT-09) was not fetched. Rows on notification semantics paraphrase only the LwM2M text.
- RFC 9148 (EST-coaps), RFC 8323, RFC 9146 and RFC 8613 were not re-read for this file. Rows cite only what C/T say about them; `standards.md` §2 has the RFC sections.
- No 1.2.x Server or BS SCR exists (§2). Item IDs for 1.2 cannot be given.
