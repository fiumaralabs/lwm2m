# LwM2M Server: Normative Standards Inventory

Scope: the Go LwM2M server that replaces Eclipse Leshan for Zephyr clients (LwM2M 1.0/1.1, CoAP/UDP, DTLS 1.2 PSK, optional CID, queue mode, SenML-CBOR, FOTA via /5).
Compiled 2026-10-06. Every OMA document marked "fetched" below was downloaded from openmobilealliance.org and text-extracted with `pdftotext`. The requirement rows in §3 paraphrase that extracted text. Nothing in §3 comes from memory.

- OMA registry clone: `scratchpad/src/lwm2m-registry` @ `7d5204dde519bc3f9ae6435d5e8d62e25da4a8dd` (2026-09-15)
- Zephyr clone: `scratchpad/src/zephyr` @ `74b7173e9c929cf8eed570fb3df099c5514720c0`, `subsys/net/lib/lwm2m`

Abbreviations: **C** = Core TS, **T** = Transport TS, **C10** = combined TS 1.0.2. Section numbers are 1.1.1 unless marked otherwise. A 1.2 renumbering is given as "1.2: §x".

---

## 1. OMA documents

Base URL: `https://www.openmobilealliance.org/release/LightweightM2M/`. This directory listing is the authoritative index. On 2026-10-06 its newest entry is `V1_2_2-20240613-A`, so **1.2.2 is the current approved release**. No 1.3 or 2.0 release exists in the directory.

### 1.1 Technical Specifications

| Ver | Document ID | Approved | URL (PDF; HTML under `HTML-Version/` where noted) | Fetched |
|---|---|---|---|---|
| 1.0.2 | OMA-TS-LightweightM2M-V1_0_2-20180209-A (single TS: core + transport) | 2018-02-09 | `.../V1_0_2-20180209-A/OMA-TS-LightweightM2M-V1_0_2-20180209-A.pdf` | yes |
| 1.0.2 | OMA-ERELD-LightweightM2M-V1_0_2-20180209-A (enabler release def.) | 2018-02-09 | `.../V1_0_2-20180209-A/OMA-ERELD-LightweightM2M-V1_0_2-20180209-A.pdf` | listed |
| 1.0.2 | 1.0 object XML (OMA-SUP-XML_LWM2M_{Security,Server,Device,Firmware_Update,...}) | 2017 | same dir, `.txt` files | listed |
| 1.0 / 1.0.1 | V1_0-20170208-A, V1_0_1-20170704-A (superseded) | 2017 | `.../V1_0-20170208-A/`, `.../V1_0_1-20170704-A/` | listed |
| 1.1 | V1_1-20180710-A (superseded by 1.1.1) | 2018-07-10 | `.../V1_1-20180710-A/` | listed |
| **1.1.1** | **OMA-TS-LightweightM2M_Core-V1_1_1-20190617-A** | 2019-06-17 | `.../V1_1_1-20190617-A/OMA-TS-LightweightM2M_Core-V1_1_1-20190617-A.pdf` (+ `HTML-Version/...Core-V1_1_1-20190617-A.html`) | yes |
| **1.1.1** | **OMA-TS-LightweightM2M_Transport-V1_1_1-20190617-A** | 2019-06-17 | `.../V1_1_1-20190617-A/OMA-TS-LightweightM2M_Transport-V1_1_1-20190617-A.pdf` (+ HTML) | yes |
| 1.1.1 | OMA-SUP-XML_LWM2M-V1_1-20180710-A.xsd (object schema) | 2018-07-10 | `.../V1_1_1-20190617-A/OMA-SUP-XML_LWM2M-V1_1-20180710-A.xsd` (registry also has `LWM2M-v1_1.xsd`) | listed |
| 1.2 | OMA-TS-LightweightM2M_Core-V1_2-20201110-A / _Transport-V1_2-20201110-A | 2020-11-10 | `.../V1_2-20201110-A/` (+ HTML) | listed |
| 1.2.1 | OMA-TS-LightweightM2M_Core-V1_2_1-20221209-A / _Transport-V1_2_1-20221209-A | 2022-12-09 | `.../V1_2_1-20221209-A/` (+ HTML) | listed |
| **1.2.2 (current)** | **OMA-TS-LightweightM2M_Core-V1_2_2-20240613-A** | 2024-06-13 | `.../V1_2_2-20240613-A/OMA-TS-LightweightM2M_Core-V1_2_2-20240613-A.pdf` (+ `HTML-Version/...Core-V1_2_2-20240613-A.html`) | yes |
| **1.2.2 (current)** | **OMA-TS-LightweightM2M_Transport-V1_2_2-20240613-A** | 2024-06-13 | `.../V1_2_2-20240613-A/OMA-TS-LightweightM2M_Transport-V1_2_2-20240613-A.pdf` (+ HTML) | yes |

Per its §1.1, the 1.2.2 changes are listed in the ERELD (`OMA-ERELD-LightweightM2M-V1_2_2-20240613-A`). The 1.2.1 fixes (C 1.2.2 §1.2) are: an optional parameter on Bootstrap-Pack-Request, LwM2M CBOR in Create, a rewrite of the attribute text (attributes moved to §7.3), unique Execute arguments, /23 excluded from the Register list, the LwM2M CBOR content-format number, and a consolidated list of endpoint-name URNs.

### 1.2 Enabler Test Specifications (ETS) and interop

All under `.../ETS/`:

| Doc | Status | Notes |
|---|---|---|
| OMA-ETS-LightweightM2M-V1_0_2-20180815-A.zip | Approved 2018-08-15 (fetched) | PDF plus `example-keys.zip`. 1.0 interop cases `LightweightM2M-1.0-int-*` |
| OMA-ETS-LightweightM2M-V1_0_1-20170926-A.pdf | Approved (superseded) | |
| OMA-ETS-LightweightM2M-V1_1-20190912-D.pdf | **Draft only** (fetched). Internal ID `OMA-ETS-LightweightM2M_INT-V1_1-20190912-D` | No approved 1.1 ETS exists. Cases `LightweightM2M-1.1-int-*` |
| **OMA-ETS-LightweightM2M_INT-V1_2-20231003-A.pdf** | **Approved 2023-10-03 (fetched). Current approved ETS** | Covers 1.0, 1.1 and 1.2 interop cases (see the test-group map below) |
| OMA-ETS-LightweightM2M_INT-V1_2_1-20240312-C.pdf | Candidate 2024-03-12 | Newest file present |
| `.../EVP/OMA-EVP-LightweightM2M-V1_0-20140819-C.pdf` | Enabler Validation Plan 1.0 (old) | |

Test-group map in ETS INT 1.2 §6 (useful as our conformance backlog):

- 0-99: Bootstrap. int-0..10, plus int-11 OSCORE, int-12 Bootstrap-Pack, int-19 MQTT.
- 100-199: Registration.
  - 101 register, 102 update, 103 deregister, 104 update trigger
  - 105 discarded update, 106 TCP, 107 lifetime, 108/109 queue mode
  - 110/111 Profile ID (1.2)
- 200-299: Device Management.
  - 201-237: per-format read/write (plain text, opaque, TLV, JSON, CBOR, SenML JSON, SenML CBOR) and composite operations
  - 241 execute, 256 write failure, 260 discover, 261 write-attributes
  - 264/266 discover depth (1.2), 270/271 create, 280/281 read-composite, 290 delete
- 300-399: Information Reporting.
  - 301-303 observe/cancel (RST and Observe=1)
  - 304-310 observe-composite, 306/307/311 Send and mute
  - 312/313 observe attributes in the Observe request (1.2)
- 400-499: Security. 401 UDP PSK, 402/403 certificate, 404/406 TCP, 405 OSCORE.
- 500-999: core objects 0-7 and multi-server (950/951).
  - FOTA cases are 751-780: push, pull, and error cases 1-9.
- 1000+: other objects (9, 10, 11, 13, 15, 16, 19, 20).

OMA TestFest / SVE (Specification Validation Event) material:

- Registration page: https://omaspecworks.org/events/testfests-registration/
- Product listing: https://www.openmobilealliance.org/specifications/resources/product-listing/
- Historical page: https://github.com/OpenMobileAlliance/technical.openmobilealliance.org/blob/master/testfest.html
- An OMA community post says SVE#40 used an ETS with new 1.2 cases (https://community.openmobilealliance.org/unlocking-utility-benefits-with-lwm2m-2).

**No per-event test descriptions are published outside `ETS/`.** The ETS INT documents above are the public interop test descriptions.

Release overview page https://www.openmobilealliance.org/specifications/lwm2m/releases/ could not be read (the fetch was truncated and the page is JS-rendered). The `/release/` directory listing was used instead.

---

## 2. IETF RFCs: sections the server must implement

Server roles: CoAP **client** for DM/IR requests and observations; CoAP **server** for /rd, /bs and /dp; DTLS **server** (T §5.2.7: the LwM2M client is always the (D)TLS client).

### RFC 7252: CoAP (MUST; T §6.1)
- §3, §3.1-3.2: message header, token length 0-8, option delta/length encoding, payload marker 0xFF.
- §4.2: CON reliability; exponential backoff with initial timeout in [ACK_TIMEOUT, ACK_TIMEOUT*ACK_RANDOM_FACTOR].
- §4.3: NON (notifications may be NON; T §6.4).
- §4.4: Message ID correlation; RST matching.
- §4.5: deduplication of CON/NON within EXCHANGE_LIFETIME/NON_LIFETIME; replay the cached response for duplicate CONs.
- §4.6: message size (≤1152 B unless PMTU known; drives Block size).
- §4.7: congestion control, NSTART=1 outstanding interaction per client. The queue-mode sequencing rule rests on this (T §6.5).
- §4.8: ACK_TIMEOUT 2 s, ACK_RANDOM_FACTOR 1.5, MAX_RETRANSMIT 4, NSTART 1, DEFAULT_LEISURE 5 s, PROBING_RATE 1 B/s.
- §4.8.2: MAX_TRANSMIT_SPAN 45 s, MAX_TRANSMIT_WAIT 93 s (queue-mode awake window, T §6.5), MAX_LATENCY 100 s, PROCESSING_DELAY 2 s, MAX_RTT 202 s, EXCHANGE_LIFETIME 247 s (bootstrap-finish timeout, C §6.1.6), NON_LIFETIME 145 s.
- §5.2.1-5.2.3: piggybacked vs separate vs NON responses. The server must accept separate responses (T §6.5).
- §5.3.1-5.3.2: tokens (generate unguessable tokens; match on token + endpoint; with DTLS also match the security session, §9.1.1).
- §5.4.1: critical vs elective options (reject unknown critical options with 4.02).
- §5.4.5-5.4.6: repeatable options (Uri-Path, Uri-Query, Location-Path).
- §5.5.4, §5.10.3-5.10.4: Content-Format and Accept negotiation.
- §5.8: GET/POST/PUT/DELETE.
- §5.9: response codes (LwM2M subset in T §6.7).
- §5.10.1: Uri-Path/Uri-Query (registration parameters go in Uri-Query).
- §5.10.7: Location-Path (registration handle under /rd).
- §5.10.9: Size1.
- §6.1-6.5: coap/coaps URIs (5683/5684), decomposing and composing URIs.
- §7.2, §7.2.1: link-format discovery and the `ct` attribute (Register payload `</>;ct=...`).
- §9.1, §9.1.1-9.1.3: DTLS-secured CoAP.
  - Messages from different DTLS sessions/epochs MUST NOT match. Peer identity comes from DTLS.
  - §9.1.3.1 PSK, §9.1.3.2 RPK, §9.1.3.3 certificates.
- §11.3-11.4: amplification and IP spoofing (relevant to NoSec and to an unauthenticated /rd).
- §12.3: Content-Format registry.

### RFC 7641: Observe (MUST; T §6.1, §6.4.5)
- §2: Observe option (0 = register, 1 = deregister; 24-bit sequence number in notifications).
- §3.1: client request (GET with Observe=0 and a fresh token).
- §3.2: notifications.
  - Match on token.
  - ACK CON notifications; send RST for unknown tokens.
  - A 2.05 without Observe ends the observation.
- §3.3-3.3.1: freshness/Max-Age. LwM2M instead relies on pmax; do not expire on Max-Age alone.
- §3.4: **reordering**. V2 is newer than V1 iff (V1<V2 and V2−V1<2^23) or (V1>V2 and V1−V2>2^23) or T2>T1+128 s.
- §3.5: transmission (CON vs NON chosen by the client).
- §3.6: **cancel**. RST in reply to a notification, or GET with Observe=1, the same token and the same options (T §6.4.5 maps both).
- §4.5: server side. A CON notification at least every 24 h. Tells us why CON notifications appear even when con is not set.

### RFC 7959: Block-wise (server MUST; T §6.1, C E.6)
- §2.1-2.2: Block1/Block2 option format (NUM/M/SZX, sizes 16-1024).
- §2.3: block options in requests vs responses.
- §2.4: Block2. Large Read/Observe responses; the client asks for later blocks.
- §2.5: Block1. FOTA push to /5/0/0, large Write/Create/Send; the server must reassemble Block1 for /dp and /rd.
- §2.6: Observe with Block2 (notification carries the first block; the client fetches the rest by GET without Observe).
- §2.7: Block1 + Block2 combined.
- §2.9.1-2.9.3: 2.31 Continue, 4.08 Request Entity Incomplete, 4.13 Request Entity Too Large (with Size1).
- §4: Size1/Size2.
- §7.1: resource exhaustion (bound reassembly buffers).

### RFC 8132: FETCH / PATCH / iPATCH (1.1 composite operations, T §6.4.4-6.4.5)
- §2, §2.1-2.6: FETCH (Read-Composite; Observe-Composite with §2.4; Block with §2.5).
- §3: iPATCH (Write-Composite). §4: method codes (FETCH 0.05, PATCH 0.06, iPATCH 0.07).

### RFC 9175: Echo and Request-Tag (SHOULD; T §5.6, §5.7, §6.1)
- §2.2-2.4: Echo (freshness). MUST be used with OSCORE on first Register/Bootstrap (T §6.4.2-6.4.3).
- §3.2-3.4: Request-Tag for Block1 integrity.
- §4: token processing for secure request/response binding.

### RFC 8323: CoAP over TCP/TLS (optional; 1.1 "T" binding, T §6.8.2)
- §3.2-3.4: framing, connection health.
- §5.3-5.6: CSM, Ping/Pong, Release, Abort.
- §6: BERT.
- §7.1-7.4: Observe over reliable transport (no reordering; cancellation).
- §8.1-8.2: `coap+tcp`/`coaps+tcp`. §9.1: TLS binding. §11.7: ALPN `coap`.

### RFC 6690: CoRE Link Format (MUST; Register/Update/Discover payloads)
- §2: ABNF (`<URI-ref>;param=value,...`). Parse quoted and unquoted values and both `ver=1.1` and `ver="1.1"`.
- §2.1: target/context. §3.1: `rt` (`oma.lwm2m` alternate path, T §6.4.1). §3.2: `if`. §3.3: `sz`.
- §4: /.well-known/core (not used by the LwM2M server). §7.3: application/link-format (ct 40).

### RFC 8428: SenML (MUST; 1.1 formats)
- §4.1: base fields (bn, bt, bu, bv, bs, bver).
- §4.2: regular fields (n, u, v, vs, vb, vd, s, t, ut).
- §4.3: labels. §4.5: records.
- §4.6: **resolved records** (bn+n concatenation, bt+t). LwM2M names are paths.
- §5: JSON (application/senml+json, **ct 110**).
- §6: **CBOR representation** (application/senml+cbor, **ct 112**). Integer labels: bver −1, bn −2, bt −3, bu −4, bv −5, bs −6, n 0, u 1, v 2, vs 3, vb 4, s 5, t 6, ut 7, vd 8.
  - LwM2M extension: object links use **string key "vlo"** in both JSON and CBOR (C §7.4.6).
  - **SenML-CBOR lives in RFC 8428 §6. There is no separate RFC for it.**
- §11: CDDL. §12.5: CoAP Content-Format registration.

### RFC 8790: FETCH and PATCH with SenML (**1.2 only**)
- SenML-ETCH JSON ct 320 and SenML-ETCH CBOR ct 322 (C 1.2.2 §7.5 Table 7.5-3).
- Used in 1.2 for Read-/Observe-/Write-Composite request bodies (T 1.2.2 §6.4.4-6.4.5).
- **Not used by LwM2M 1.1.** 1.1 composite bodies use plain SenML 110/112.

### RFC 7049 / RFC 8949: CBOR
- C §7.4.3 (single-resource CBOR, ct 60) and the SenML-CBOR encoding.
- 1.1.1 cites 7049. RFC 8949 obsoletes it on the wire without changes; use 8949 §3 (major types), §3.4 (tags) and §4.2 (deterministic encoding; Zephyr builds with ZCBOR_CANONICAL).

### RFC 6347: DTLS 1.2 (MUST; T §5.2.1)
- §3.2-3.3: handshake reliability, replay detection.
- §4.1, §4.1.1.1: record layer, PMTU.
- §4.1.2.6: **anti-replay window**. Window size 1 removes the block-interchange attack (T §5.6).
- §4.1.2.7: drop records with a bad MAC silently (also T §5.2.9 note).
- §4.2.1: **HelloVerifyRequest cookie**. Mandatory DoS countermeasure for the server.
- §4.2.3: handshake fragmentation and reassembly.
- §4.2.4: timeout and retransmission (flight timers).
- §4.2.8: new association with existing parameters. A ClientHello with epoch 0 on a live 5-tuple restarts the session; after that, expect a re-Register (T §6.4.3).

### RFC 9146: DTLS 1.2 Connection ID (Zephyr `CONFIG_LWM2M_DTLS_CID`, default y when mbedTLS supports it; LwM2M 1.2 T §5.2.8)
- §3: `connection_id` extension (type 54). Each peer announces the CID it wants to **receive**.
  - Zephyr usually offers a zero-length CID, so the **server must issue a non-zero CID** for NAT rebinding to work.
- §4: record layer (content type `tls12_cid` = 25; inner plaintext with real content type and padding).
- §5.1-5.3: payload protection (MAC/AEAD additional data include the CID).
- §6: **peer address update**. Switch the send address only if the record verifies, is newer (epoch, seq), and the address is reachable. The LwM2M registration address (C §6.2.1) must follow it.
- §10.2-10.3: IANA values.
- 1.1.1 does not mention CID; it arrives normatively in 1.2 (T 1.2.2 §5.2.8, reference [DTLS-1.2-CID] = RFC 9146).
- mbedTLS can also speak the pre-RFC draft ("legacy" compat mode). Unverified for our Zephyr build; check the Kconfig before interop.

### RFC 7925: TLS/DTLS IoT profile (SHOULD conform; T §5.2.1)
- §4.2: **PSK**.
  - Mandatory-to-implement TLS_PSK_WITH_AES_128_CCM_8.
  - Identities up to 128 bytes, keys up to 64 bytes (repeated as MUST in T §5.2.8.1).
  - No PSK identity hint required.
- §4.3: RPK (TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, secp256r1). §4.4: certificates (profile, no revocation requirement).
- §5: signature_algorithms. §6: error handling/alerts (LwM2M alert table in T §5.2.9).
- §7: **session resumption** (MUST for constrained clients; RFC 5077 tickets optional). This is what Zephyr's queue mode wakes into (T §6.5 step 3).
- §10: keep-alive.
- §11: timeouts (long DTLS retransmission timers for LPWAN; the server must tolerate slow flights).
- §13: truncated MAC / Encrypt-then-MAC. §14: SNI (MUST when DNS is absent, T 1.2.2 §5.2.9.6.1).
- §15: max fragment length. §16: session hash, extended master secret (RFC 7627).
- §17-19: renegotiation (disable), downgrade, crypto agility.

### PSK suites and key exchange
- **RFC 4279** (PSK key exchange): §2 PSK key exchange; §5.1 identity encoding (UTF-8 to octets); §5.2 hint (do not rely on it); §5.3-5.4 implementation and management requirements.
- **RFC 6655**: TLS_PSK_WITH_AES_128_CCM_8 = {0xC0,0xA8} (server MUST, T §5.2.8.1).
- **RFC 5487**: TLS_PSK_WITH_AES_128_CBC_SHA256 = {0x00,0xAE} (server MUST, T §5.2.8.1; client SHOULD NOT use it, per RFC 7457).
- **RFC 7251**: TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 = {0xC0,0xAE} (server MUST for RPK and X.509, T §5.2.8.2-5.2.8.3).
- **RFC 5289**: TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 = {0xC0,0x23} (server MUST for RPK and X.509).
- Curves: secp256r1 SHALL be supported; groups under 255 bits SHALL NOT be (T §5.2.3).

### RFC 7250: Raw Public Keys (server MUST support the RPK mode, T B.2.1 LwM2M-SEC-003-S-M)
- §3: client/server_certificate_type extensions. §4.1-4.4: handshake behaviour.
- RPK as SubjectPublicKeyInfo. The server MUST check the client RPK exactly against the stored key (T §5.2.8.2).

### Other referenced
- RFC 5280 (X.509; T §5.2.8.3). RFC 6066 (SNI; T 1.2.2 §5.2.9.6.1). RFC 5958 (private key format in /0/x/5).
- RFC 3986 (URIs). RFC 6920 (named-information hash IDs for the 1.2 Profile ID, C 1.2.2 §6.2.1).

### RFC 8613: OSCORE (optional; 1.1 T §5.5, object 21)
- §3: security context derivation. §5-6: COSE object and option compression.
- §7.2-7.5: sequence numbers, freshness, replay.
- §8: processing. The Echo requirement on the first use of a context comes from T §6.4.2-6.4.3.
- Not shipped by Zephyr (object 21 ID only; see §4).

### RFC 9147: DTLS 1.3 (future; 1.2 T §5.2.1 "MAY optionally use")
- §4: unified record header. §5.1: cookie (HRR).
- §7: ACK. §8: KeyUpdate. §9: CID updates (native CID).
- §5.11: new associations with existing parameters.
- Also RFC 8446 (TLS 1.3).

---

## 3. Server-side normative requirements checklist

Primary source is 1.1.1 (C = Core, T = Transport). "1.0" points to C10 (OMA-TS-LightweightM2M-V1_0_2): §5.1 attributes, §5.2 bootstrap, §5.3 registration, §5.4 DM, §5.5 IR, §6 identifiers/formats, §7 security, §8.2 URI mapping, §8.3 queue mode, §8.5 response codes.
The Ver column says where the requirement applies. Δ1.2 flags a 1.2.x change; 1.2.2 section numbers are given where they differ.

### 3.1 General / compatibility / CoAP mapping

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| GEN-01 | Server MUST support clients of its own major.minor version and SHOULD support all clients of the same major version (a 1.1 server SHOULD support 1.0 clients). | C §5.2.1 (1.2: §5.1) | 1.1, 1.2 |
| GEN-02 | Server MUST, where possible, silently ignore transactions on optional objects, object versions or resources it does not support. | C §5.2.2, §5.2.3 | 1.1, 1.2 |
| GEN-03 | All LwM2M operations MUST be CoAP CON, except Notify and Execute, which MAY be NON. | T §6.4 (C10 §8.2) | all |
| GEN-04 | Server MUST support CoAP (RFC 7252), Observe (RFC 7641) and Block-wise (RFC 7959). | T §6.1 (C10 §8.1) | all |
| GEN-05 | Server MUST support the UDP binding (coap:// 5683, coaps:// 5684) and SHOULD support SMS. Δ1.2: wording is "Server MUST support UDP". | T §6.8, §6.8.1 | all |
| GEN-06 | T §6.7 lists the only valid response codes. Clients return 5.00 for errors with no more specific code; the server must map every one of them. | T §6.7 | 1.1, 1.2 |
| GEN-07 | Server SHOULD implement the Echo option (freshness) and, when doing block-wise, Request-Tag (RFC 9175). | T §5.6, §5.7, §6.1 | 1.1, 1.2 |
| GEN-08 | Alternate path: if the Register/Update payload has a link with `rt="oma.lwm2m"`, the server MUST prefix all DM/IR request paths with it (e.g. `GET /lwm2m/3/0/0`). Bootstrap paths are never prefixed. | T §6.4.1 | 1.1, 1.2 |
| GEN-09 | Path syntax violations (e.g. a Resource Instance without a Resource) are answered 4.05 by the client. The server SHOULD validate paths before sending. | T §6.4.4; C §6.3.1, §6.4.1 | 1.1, 1.2 |
| GEN-10 | Server SHOULD NOT send DM or IR requests before it has answered the Register. Clients ignore them before registration completes. | C §6.3, §6.4 | all |

### 3.2 Registration interface (/rd)

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| REG-01 | Register = CoAP POST `/rd?ep={ep}&lt={s}&lwm2m={ver}&b={binding}&Q&sms={msisdn}` with an application/link-format payload. The server MUST support every parameter in Table 6.2.1-1. | C §6.2.1; T §6.4.3, Table 6.4.3-1 | all |
| REG-02 | `ep` is optional for 1.1+ clients but the server MUST support it. If absent, the server identifies the client by its authenticated security identity (PSK identity, cert CN, RPK). 1.0: `ep` is required. | C §6.2.1, §7.3; C10 §5.3.1 | 1.0 req / 1.1+ opt |
| REG-03 | `lt` and `lwm2m` are required parameters. `lt` must equal /1/x/1, and writing /1/x/1 changes the registration lifetime. | C §6.2, Table 6.2.1-1 | all |
| REG-04 | Server MUST refuse a Register whose `lwm2m` version it does not support, with 4.12 Precondition Failed. | C §6.2.1; T Table 6.7-2 | 1.1, 1.2 |
| REG-05 | Success is 2.01 Created with Location-Path. Server MUST return a location under `/rd` (e.g. `/rd/5a3f`). | T §6.4.3 | all |
| REG-06 | Register error codes: 4.00 (mandatory param missing or unknown param, unknown ep, ep ≠ cert CN or security identity), 4.03 (ep not allowed), 4.09 (conflicts with server config), 4.12 (version). | T §6.7 Table 6.7-2 | 1.1, 1.2 |
| REG-07 | Payload MUST be application/link-format (ct 40). Each link target is an object path with an optional `ver`. Other link params MUST be silently ignored unless the enabler defines them (`ct` on `</>`, `rt="oma.lwm2m"`). | C §6.2.1 | all |
| REG-08 | Unknown objects or unsupported object versions in Register/Update MUST be silently ignored and MUST NOT cause rejection. Δ1.2: worded "MUST NOT be interpreted as an error". | C §6.2.1, §6.2.2 | all |
| REG-09 | The client list excludes /0 and /21 (Δ1.2 also /23) and MUST include /1 and /3. The server must not expose /0 even if a buggy client lists it. | C §6.2.1 | all |
| REG-10 | The client MAY advertise optional formats as `</>;ct=110` or `ct="110 112 60"` (quoted list). The server should use them for Accept and Content-Format choices. | C §6.2.1 (C 1.2.2 §6.2.1 example) | 1.1, 1.2 |
| REG-11 | Server records the source address and port (or MSISDN) of the Register and uses it for all later interactions. With CID this is the address after an RFC 9146 §6 update. | C §6.2.1 | all |
| REG-12 | A Register from a client that is already registered: the server removes the old registration (and its observations) and processes the new one. | C §6.2.1; C §6.4.1 | all |
| REG-13 | Lifetime expiry without an Update: server MUST remove the registration and its observations. A later Update to a removed registration gets an error (4.04). | C §6.2; T Table 6.7-2 | all |
| REG-14 | Update = POST `/{location}?lt&b&sms` carrying only the changed params plus an optional object-list payload. An empty Update refreshes the lifetime. Replies: 2.04; 4.00 bad param; 4.04 unknown location. Δ1.2: adds Profile ID. | C §6.2.2, Table 6.2.2-1; T §6.4.3 | all |
| REG-15 | An object list in an Update is complete: it replaces the stored list rather than being a diff. | C §6.2.2 | all |
| REG-16 | De-register = DELETE `/{location}`. Replies: 2.02; 4.04 unknown; 4.00. The server removes the registration, and observation state is void. | C §6.2.3, §6.4.1 note; T §6.4.3 | all |
| REG-17 | A new (D)TLS security context makes the client register again (Update alone is not enough). In NoSec, an IP change does the same. The server may tie a registration to its DTLS session. | T §6.4.3 | 1.1, 1.2 |
| REG-18 | Binding values. 1.0: U, UQ, S, SQ, US, UQS (UQSQ and USQ unsupported), queue mode inside `b`. 1.1: U, T, S, N plus a separate `Q` flag. Δ1.2: adds M (MQTT) and H (HTTP). The server must parse both styles. The client SHALL assume the server supports U. | C10 §5.3.1.1 Table 8; C §6.2.1.2; C 1.2.2 §6.2.1.2 | per ver |
| REG-19 | Registration Update Trigger: Execute /1/x/8 (1.1 adds an optional binding-override argument). Bootstrap-Request Trigger is /1/x/9 (1.1). | C §6.2.1.2, §6.2.2; E.2 | 1.0 (/8), 1.1+ |
| REG-20 | Registration-sequence resources (/1/x/13-20: priority order, initial delay, failure block, bootstrap on failure, retry count/timer, sequence delay/retry) are client behaviour. Server support for these "registration control resources" is optional. | C §6.2.1.1; C B.2.4 LwM2M-CR-011-S-O | 1.1, 1.2 |
| REG-21 | Δ1.2 Profile ID: the server MUST support Register params and payloads except Profile ID, which it MAY refuse. If the server cannot resolve the profile, or no list is given, it MUST reply with an error and the client re-registers with a full list. The payload becomes optional. | C 1.2.2 §6.2.1 | 1.2 |
| REG-22 | Server MUST support the SMS Number param. It only matters for SMS trigger or binding (out of scope for UDP-only deployments, but parse and store it). | C B.2.4 LwM2M-CR-006-S-M; T §6.6 | all |

### 3.3 Device Management & Service Enablement

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| DM-01 | Server MUST support Read, Discover, Write, Write-Attributes, Execute, Create and Delete. Read-Composite and Write-Composite are optional for the server. | C §6.3; C B.2.5 (DMSE-001..014) | all (composite 1.1+) |
| DM-02 | Read = GET /o[/i[/r[/ri]]] (resource instance level is 1.1+) with optional Accept. Returns 2.05, or 4.00, 4.01, 4.04, 4.05, 4.06 (Accept unsupported). An empty 2.05 is valid (object with no instances). | T §6.4.4, Table 6.4.4-1, §6.7 Table 6.7-3 | all |
| DM-03 | Server MUST silently ignore unsupported optional resources in Read, Discover and Send payloads. | C §6.3.1, §6.3.2, §6.4.6 | 1.1, 1.2 |
| DM-04 | Discover = GET with Accept application/link-format. The response carries attributes scoped to the requesting server only (object, instance or resource level, with `dim` for multi-instance). Δ1.2: `depth` param (client SHOULD support; default per Table 6.3.2-2). | C §6.3.2; C 1.2.2 §6.3.2 | all |
| DM-05 | Write: PUT = Replace, POST = Partial Update (object instance only; format MUST be TLV, SenML CBOR or SenML JSON, and Δ1.2 also LwM2M CBOR). Content-Format MUST be set. Multi-value writes MUST use TLV, SenML or (1.2) LwM2M CBOR. | C §6.3.3; T §6.4.4 | all |
| DM-06 | Write replies: 2.04, 2.31 (block), 4.00, 4.01, 4.04, 4.05, 4.08, 4.13, 4.15. The client rejects a value that is the wrong type or out of range, or an Objlnk pointing at nothing. The server must validate against the object model before sending. | C §6.3.3, §7.1; T Table 6.7-3 | all |
| DM-07 | Write-Attributes = PUT `/path?pmin&pmax&gt&lt&st&epmin&epmax` (1.2 adds edge, con, hqmax). Only `<NOTIFICATION>` attributes are writable. A parameter with no value unsets it. Inconsistent sets get 4.00. Values are scoped per server. | C §6.3.4, §5.1.2; T §6.4.4 | all (epmin/epmax 1.1+) |
| DM-08 | Execute = POST /o/i/r. Arguments are plain text in the C §6.3.5 ABNF (`0='a',1`) or SenML. Executing on an object, instance or resource instance is an error. Replies: 2.04, 4.00, 4.01, 4.04, 4.05. Δ1.2.1: arguments must be unique. | C §6.3.5; T §6.4.4 | all |
| DM-09 | Create = POST /o with TLV, SenML (or Δ1.2 LwM2M CBOR) and all mandatory resources. If no instance ID is given, the client assigns one and returns 2.01 with Location. Single-instance objects get ID 0. A conflicting instance ID gets 4.00. Only objects announced at Register or Update can be created. | C §6.3.6, §8.2.3; T §6.4.4 | all |
| DM-10 | Delete = DELETE /o/i (Δ bootstrap only: /o and /). /3/0 cannot be deleted. The target must be an announced instance. | C §6.3.7 | all |
| DM-11 | Operations on /0 (and /21 OSCORE) get 4.01 Unauthorized from the client. The server MUST NOT issue them outside bootstrap. | C §6.3, §6.4 | all |
| DM-12 | Read-Composite = FETCH with a SenML JSON/CBOR path list; non-atomic, best effort. Write-Composite = iPATCH with SenML; atomic, all-or-nothing. Δ1.2: SenML-ETCH (ct 320/322) and LwM2M CBOR. | C §6.3.8, §6.3.9; T Table 6.4.4-1; C 1.2.2 §6.3.8-6.3.9 | 1.1, 1.2 |
| DM-13 | Object-level Write, Execute or Delete is answered 4.05 by the client. Object-level Discover and Write-Attributes need no access right. | C §8.2.3 | all |
| DM-14 | Access control is enforced by the client (/2). The server must handle 4.01 and may manage ACLs when it is the Access Control Owner (C §8.1.2.2). | C §8, §8.2 | all |

### 3.4 Attributes and Information Reporting

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| ATT-01 | Server MUST support every `<NOTIFICATION>` attribute (pmin, pmax, gt, lt, st, epmin, epmax; Δ1.2 also edge, con, hqmax) and the `<PROPERTIES>` attributes (dim, ssid, uri, ver, lwm2m). | C §5.1.2; C B.2.1 ATTR-001..005; C 1.2.2 §7.3.1-7.3.2 | all |
| ATT-02 | Precedence: a lower level overrides a higher one (resource instance > resource > object instance > object). Defaults come from the server account: pmin = /1/x/2, pmax = /1/x/3. | C §5.1.1; C 1.2.2 §7.3.2 | all |
| ATT-03 | pmin: the client waits at least pmin seconds between notifications; a change inside pmin is sent when pmin expires. Default 0. | C §5.1.2 Table 5.1.2-2 | all |
| ATT-04 | pmax: a notification MUST go out when pmax elapses after the last one. pmax 0 or absent means no maximum. pmax < pmin is ignored. Δ1.2: pmax ≥ pmin (equality allowed). | C §5.1.2; C 1.2.2 §7.3.2 | all |
| ATT-05 | gt and lt: notify on crossing the threshold, subject to pmin. st: notify when the change since the last notification is ≥ st. Applies to numeric resources only. | C §5.1.2 | all |
| ATT-06 | Validity: lt < gt and lt + 2·st < gt. A single Write-Attributes violating this MUST be rejected (4.00). The server should validate before sending. | C §5.1.2; T §6.4.4 | all |
| ATT-07 | epmin and epmax bound evaluation (not notification) intervals; epmin < epmax. | C §5.1.2 | 1.1, 1.2 |
| ATT-08 | Δ1.2 edge (Boolean rising or falling), con (force CON notifications), hqmax (historical queue while offline; relates to /1/x/6 Notification Storing). | C 1.2.2 §7.3.2 Table 7.3.2-1 | 1.2 |
| OBS-01 | Observe = GET with Observe=0 on /o, /o/i, /o/i/r or /o/i/r/ri. The token matches notifications. The first 2.05 carries Observe. Errors: 4.00, 4.01, 4.04, 4.05, 4.06. | T §6.4.5, Table 6.4.5-1 | all |
| OBS-02 | Cancel: either a RST in reply to a notification (the client MUST cancel for CON and NON alike) or GET with Observe=1 on the same path. | T §6.4.5; RFC 7641 §3.6 | all |
| OBS-03 | Server MUST re-establish the observations it wants on **every** Register; the old ones are void. | C §6.4.1 | all |
| OBS-04 | Notify carries the new value (2.05, CON or NON). An observation on an object or instance reports all readable resources. With multiple servers, the client suppresses Notify without Read rights. | C §6.4.2, §8.2.4; T §6.4.5 | all |
| OBS-05 | Observe-Composite = FETCH with Observe=0 and a SenML path list (Δ1.2 SenML-ETCH). Cancel-Composite = FETCH with Observe=1 and exactly the same list. Server support is optional. The server is responsible for consistent pmin/pmax across the set. | C §6.4.4-6.4.5; T Table 6.4.5-1; C B.2.6 IR-004/005-S-O | 1.1, 1.2 |
| OBS-06 | Δ1.2: attributes MAY be passed as query params on the Observe request and then override the attached attributes for that observation. A client without support MUST reject. | C 1.2.2 §6.4.1 | 1.2 |
| SEND-01 | Send = client POST `/dp` with Content-Format SenML JSON or SenML CBOR (Δ1.2 also LwM2M CBOR). Reply 2.04; 4.00 on any rule violation; 4.04 if a reported object was never registered. Server support is optional. | C §6.4.6; T §6.4.5, Table 6.7-4; C B.2.5 DMSE-015-S-O | 1.1, 1.2 |
| SEND-02 | Server MUST reject a Send containing unregistered object instances. Unknown optional resources are silently ignored. Mute is controlled with /1/x/23 (Mute Send). | C §6.4.6 | 1.1, 1.2 |

### 3.5 Queue mode

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| QM-01 | Server MUST support Queue Mode (client SHOULD). 1.0: signalled by `b=UQ` (or SQ, UQS). 1.1+: signalled by the `Q` query flag. | T §6.5; C10 §8.3, §5.3.1.1; C B.2.8 MEC-001-S-M | all |
| QM-02 | While the client is offline the server holds downlink requests. It sends them only after receiving a message from the client (typically a CON Update), one at a time (NSTART=1, wait for each response). | T §6.5 | all |
| QM-03 | Client stays awake MAX_TRANSMIT_WAIT (93 s) after its last message; Zephyr uses `CONFIG_LWM2M_QUEUE_MODE_UPTIME`, default 93. The server should time out the awake window on the same basis. | T §6.5; RFC 7252 §4.8.2 | all |
| QM-04 | When the CoAP retransmissions for a request fail, the server must report the failure to the application (the API is out of scope). | T §6.5 | all |
| QM-05 | On wake, IP or port may change. Without CID that means a new DTLS handshake (resumption RECOMMENDED) followed by Register (REG-17); with CID it is an address update (RFC 9146 §6). | T §6.5 step 3; T 1.2.2 §5.2.8 | all; CID 1.2 |
| QM-06 | Firewall and NAT binding: inbound UDP must be allowed back to the client's address and port for ≥240 s. Queue mode supplies NAT traversal by having the client speak first. | T §6.2, §6.3 | 1.1, 1.2 |

### 3.6 Bootstrap interface (Bootstrap-Server)

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| BS-01 | Bootstrap-Server MUST support Client Initiated Bootstrap: POST `/bs?ep={ep}&pct={ct}` (pct is 1.1+). Replies: 2.04; 4.00 unknown ep; 4.15 unsupported pct. | C §6.1, B.3 BOOT-001-BS-M; T §6.4.2, Table 6.4.2-1 | all (pct 1.1) |
| BS-02 | Preferred content format: 1.1 SenML JSON, SenML CBOR or TLV (C §6.1.7.1). | C §6.1.7.1 | 1.1, 1.2 |
| BS-03 | Bootstrap-Write = PUT /o[/i[/r]]. The client writes regardless of whether the instance exists and regardless of ACL. Object-level writes carry the instances in TLV or SenML. | C §6.1.7.5; T Table 6.4.2-1 | all |
| BS-04 | Bootstrap-Delete = DELETE /o/i, /o or `/`. The exceptions are the Bootstrap-Server account (with its /21 instance) and /3/0, which are never deleted. Unbootstrapping cascades on the client. | C §6.1.7.6; T §6.4.2, §5.2.5 | all |
| BS-05 | Bootstrap-Discover = GET with Accept link-format on /o or `/` (1.1). The response includes `lwm2m=`, `ssid` and `uri` attributes. | C §6.1.7.3; T Table 6.4.2-1 | 1.1, 1.2 |
| BS-06 | Bootstrap-Read = GET on /1 or /2 only (C §6.1.7.4 says "1 or 2"; T Table 6.4.2-1 says "MUST be 2": the documents disagree, so accept both). The server ignores unsupported optional resources in the reply. | C §6.1.7.4; T Table 6.4.2-1 | 1.1, 1.2 |
| BS-07 | Bootstrap-Finish = POST `/bs` with an empty payload. Bootstrap-Server MUST send it after the last write. The client answers 2.04, 4.00, or 4.06 (inconsistent config); the server MAY fix the config and retry. | C §6, §6.1.6; T §6.4.2, Table 6.7-1 | all |
| BS-08 | Client deems bootstrap failed if no Finish arrives within EXCHANGE_LIFETIME (247 s). The bootstrap session must finish well inside that. | C §6.1.6 | 1.1, 1.2 |
| BS-09 | Bootstrap-Server uses only `/{oid}/{iid}/{rid}` paths (never the alternate path). | T §6.4.1 | 1.1, 1.2 |
| BS-10 | Bootstrap-Server MUST support bootstrapping with PSK, RPK and certificates. A PSK for a bootstrap account must be high-entropy. | T §5.2.4 | 1.1, 1.2 |
| BS-11 | Server Initiated Bootstrap (the server executes /1/x/9 Bootstrap-Request Trigger) MUST be supported by the LwM2M Server. | C B.2.3 BOOT-005-S-M; C §6.1.3.4 | 1.1, 1.2 |
| BS-12 | Δ1.2 Bootstrap-Pack-Request. A BS that does not support it MUST reply "not implemented" (5.01); one that refuses it MUST reply 4.05. The pack payload is SenML or LwM2M CBOR, and the BS account is listed as link-format without params. | C 1.2.2 §6.1.7.7; T 1.2.2 §6.4.2 | 1.2 |
| BS-13 | With OSCORE, the BS MUST use Echo on Bootstrap-Request. | T §6.4.2 | 1.1, 1.2 |

### 3.7 Security

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| SEC-01 | Mutual authentication before any data exchange; all traffic encrypted and integrity-protected; replay protection; responses bound to requests. | T §5.1 | all |
| SEC-02 | The LwM2M client is always the (D)TLS client, and the Server and Bootstrap-Server are always (D)TLS servers. | T §5.2.7 | 1.1, 1.2 |
| SEC-03 | Server MUST support the PSK, RPK and X.509 modes. 1.0: PSK MUST; RPK and certificate suites are mandatory only if the mode is supported. | T B.2.1 (SEC-002/003/004-S-M); C10 §7.1 | 1.1, 1.2 / 1.0 |
| SEC-04 | PSK: the server MUST support TLS_PSK_WITH_AES_128_CCM_8 (0xC0A8) and TLS_PSK_WITH_AES_128_CBC_SHA256 (0x00AE). Zephyr 1.1 clients MUST support CCM_8. | T §5.2.8.1; C10 §7.1 | all |
| SEC-05 | PSK identity is stored in /0/x/3 ("Public Key or Identity") and the key in /0/x/5. The server MUST support arbitrary identities up to 128 bytes and keys up to 64 bytes. /0/x/4 is unused in PSK mode. | T §5.2.8.1; RFC 7925 §4.2 | all |
| SEC-06 | **Endpoint binding**: the server MUST compare `ep` against the identity authenticated in the (D)TLS handshake (PSK identity, cert CN, RPK) by equality or a lookup table, and reply 4.00 on mismatch. `ep` MUST NOT be trusted alone. | T §5.2.6; C §7.3.1; C10 §7 | all |
| SEC-07 | NoSec mode (/0/x/2 = 3) is allowed only with lower-layer security. The server MUST compare `ep` with the network-access identity. | T §5.3; T 1.2.2 §5.2.9.4 | all |
| SEC-08 | Security mode values (/0/x/2): 0 PSK, 1 RPK, 2 Certificate, 3 NoSec, 4 Certificate with EST (1.1+). | T §5.2.4 | all (4 = 1.1+) |
| SEC-09 | RPK: the server keeps its own key pair and a stored copy of each client key, and MUST check an exact match. Suites: ECDHE_ECDSA_AES_128_CCM_8 (0xC0AE) and ECDHE_ECDSA_AES_128_CBC_SHA256 (0xC023). Curve secp256r1. | T §5.2.8.2, §5.2.3 | all |
| SEC-10 | X.509: same suites as RPK, RFC 5280 / RFC 7925 profile. Client cert CN SHOULD carry `ep` (Δ1.2 explicit). Certificate Usage /0/x/15 (1.1). | T §5.2.8.3, §5.2.8.7; T 1.2.2 §5.2.9.2 | all |
| SEC-11 | Keys are unique per client; Server, BS and clients use different key pairs. Keep DTLS state for as long as is safe (session resumption). | T §5.2.1, §5.2.4 | all |
| SEC-12 | TLS alert handling table: for example, unknown_psk_identity(115) is "Fail" (the client re-bootstraps), and bad_record_mac is "Retry". Silently drop records with bad MACs. | T §5.2.9 Table 5.2.9-1 | 1.1, 1.2 |
| SEC-13 | Δ1.2: TLS 1.3 / DTLS 1.3 MAY be used. DTLS 1.2 CID (RFC 9146) is defined. /0 gains TLS/DTLS configuration resources (ciphersuites, groups, signature algorithms, TLS 1.3 features, CID flag in the bitmask). | T 1.2.2 §5.2.1, §5.2.8; C 1.2.2 E.1 | 1.2 |
| SEC-14 | SNI MUST be supported when deploying without DNS (Δ1.2 explicit). | T 1.2.2 §5.2.9.6.1 | 1.2 |
| SEC-15 | OSCORE (optional): an OSCORE Sender ID ≠ ep means the server MUST compare `ep` with the Sender ID, giving 4.00 on mismatch. Echo is required on the first message. | T §5.5.5, §6.4.3 | 1.1, 1.2 |

### 3.8 Content formats / data types / identifiers / object versioning

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| FMT-01 | Server MUST support **all** data formats: plain text (0), link-format (40), opaque (42), TLV (11542), legacy JSON (11543), CBOR (60), SenML JSON (110), SenML CBOR (112). Δ1.2 adds LwM2M CBOR (11544) and SenML-ETCH JSON/CBOR (320/322). | C §7.4; C B.2.7 IDT-001..008-S-M; C 1.2.2 §7.5 Tables 7.5-1..3 | all |
| FMT-02 | A 1.1 server MUST accept both application/senml+json and application/vnd.oma.lwm2m+json. A 1.1 client MUST NOT send the latter. | C §7.4.5 | 1.1, 1.2 |
| FMT-03 | Every message with data MUST carry Content-Format. The server MAY send Accept; the client rejects an unsupported Accept with 4.06, or picks its own format when there is no Accept. | C §7.4; T §6.4.4 | all |
| FMT-04 | TLV: an Object Instance TLV wraps the payload whenever no instance ID is in the request; a Multiple Resource TLV is used for any multi-instance resource (0..n instances). Bits 2-0 are ignored when there is a length field. | C §7.4.4 | all |
| FMT-05 | SenML (JSON and CBOR): breadth-first traversal; each object instance appears at most once; names are resolved paths (bn + n); "vlo" carries Objlnk as "oid:iid"; opaque values are base64 in JSON and byte strings in CBOR. | C §7.4.5, §7.4.6; RFC 8428 §4.6, §6 | 1.1, 1.2 |
| FMT-06 | Legacy pre-IANA numbers 1541/1542/1543 (old plain text/TLV/JSON) are still parsed by Zephyr (`LWM2M_FORMAT_OMA_OLD_*`). Tolerating them on input is recommended for interop; no OMA text requires it. | Zephyr `lwm2m_message_handling.c` | 1.0 interop |
| DT-01 | Data types: String (UTF-8), Integer (signed 8/16/32/64), Unsigned Integer (1.1+), Float (32/64), Boolean, Opaque, Time (signed Unix seconds), Objlnk (oid:iid; MAX_ID:MAX_ID = null), Corelnk (1.1+), none (executables). | C Appendix C, Table C-1 | all (uint, corelnk 1.1+) |
| DT-02 | Per-format encodings are fixed by Table C-2. TLV integers are 1/2/4/8-byte big-endian; Boolean is 1 byte; text Float is plain decimal; SenML Time is a number; CBOR Time may be a tag-1 epoch. The server MUST type-check retrieved values. | C Appendix C; C §7.1 | all |
| ID-01 | Object, instance, resource and resource-instance IDs are 16-bit; 65535 (MAX_ID) is reserved. Short Server ID range 1-65534. | C §7.3, Table 7.3-1 | all |
| ID-02 | `ep` MUST be unique on the server. URN forms are RECOMMENDED (urn:uuid, urn:dev:ops/os, urn:imei, urn:imei-msisdn, urn:imei-imsi, urn:esn, urn:meid, urn:nai). Guard database lookups (injection). | C §7.3.1, Table 7.3.1-1 | all |
| VER-01 | The server MUST be able to determine each registered object's version unambiguously. Clients send `;ver=X.Y` in Register and Discover: MUST for non-enabler objects above 1.0, MAY otherwise. A missing `ver` means 1.0, or the version tied to the client's `lwm2m` for core objects. | C §7.2.1-7.2.3, Table 7.2.3-1 | 1.1, 1.2 (1.0: C10 §6.2) |
| VER-02 | URN format `urn:oma:lwm2m:{oma,ext,x}:ObjectID[:version]`. The server ignores minor-version differences within a major (example a in C §7.2.3). | C §7.2.1-7.2.2 | 1.1, 1.2 |

### 3.9 Firmware Update (/5) server obligations

| ID | Requirement | Spec § | Ver |
|---|---|---|---|
| FW-01 | Server MUST support block-wise transfer; a client implementing /5 MUST as well. | C E.6; T §6.1 | all |
| FW-02 | Push: Write to /5/0/0 (Package) with Block1. The C E.6.2 example uses POST with Block1 128 B; Leshan uses PUT, so accept 2.31 for both. Pull: Write /5/0/1 (Package URI) and the client downloads (CoAP(S) block2 recommended; HTTP(S) or CoAP over TCP per /5/0/8). | C E.6, E.6.2 | all |
| FW-03 | Server MUST NOT put a URI in /5/0/1 whose scheme the client does not support (/5/0/8 Firmware Update Protocol Support). The server MUST ignore unknown /5/0/8 values. | C E.6 (res 1, 8) | 1.1, 1.2 |
| FW-04 | State machine: /5/0/3 State (0 Idle, 1 Downloading, 2 Downloaded, 3 Updating) and /5/0/5 Update Result (0-9; /5 v1.1+ adds 10 cancelled, 11 deferred). Errors are reported **only** through Update Result. Writing an empty string to /5/0/1 or a NUL byte to /5/0/0 resets to Idle. Execute /5/0/2 starts the update. | C E.6, E.6.1 | all |
| FW-05 | After an update, the client re-registers if its object set changed and deletes instances of objects that are no longer supported. The server must reconcile. | C E.6.3 | 1.1, 1.2 |
| FW-06 | Δ1.2 /5 v1.2 adds Cancel (10), Severity (11), Last State Change Time (12), Maximum Defer Period (13) (all from v1.1) and Automatic Upgrade at Download (14). | registry `5.xml`, `version_history/5-1_1.xml` | 1.1 obj, 1.2 |

### 3.10 Interface support matrix (Server SCR, C Appendix B.2 / T Appendix B.2)

- **M**: ATTR-001..005; INTR-001.
- **M**: BOOT-005 (server initiated), BOOT-010 (bootstrap security).
- **M**: CR-001..010 (Register, ep, lt, lwm2m, b, sms, objects, Update, De-register, expiry cleanup). **O**: CR-011.
- **M**: DMSE-001..012. **O**: DMSE-013/014/015 (Read-Composite, Write-Composite, Send).
- **M**: IR-001..003. **O**: IR-004/005 (composite).
- **M**: IDT-001..008 (plain text, opaque, TLV, JSON, CBOR, data types, unique client ID, SenML).
- **M**: MEC-001 queue mode, MEC-002 UDP. **O**: MEC-003 SMS.
- **M**: OBJ-001/002/004 (Security, Server, Device). **O**: 003, 005-009.
- **M** (T B.2.1): SEC-002/003/004 (PSK, RPK, X.509).

---

## 4. OMA objects relevant to the server (registry clone and Zephyr)

Registry layout: `<id>.xml` at the root holds the latest version, and `version_history/<id>-<maj>_<min>.xml` holds each version. Other files at the root: `DDF.xml` (index), `Common.xml` (reusable resources), `LWM2M.xsd`, `LWM2M-v1_1.xsd`, `LWM2M_senml_units.xml`. Resource counts are `<Item>` elements. "LwM2M" is the `<LWM2MVersion>` field of the latest file. The URN form is `urn:oma:lwm2m:oma:<id>[:<ver>]` (e.g. `3.xml` → `urn:oma:lwm2m:oma:3:1.3`).

Zephyr default is **LwM2M 1.0** (`Kconfig` choice `LWM2M_VERSION`, default `LWM2M_VERSION_1_0`); 1.1 is opt-in. Zephyr has **no** 1.2 support: no LwM2M CBOR writer and no `lwm2m=1.2`.
Zephyr formats: TLV, OMA JSON, SenML JSON, CBOR, SenML CBOR, plain text, opaque, link-format (`lwm2m_rw_*.c`). Version 1.1 implies `LWM2M_RW_CBOR_SUPPORT`; SenML CBOR is a separate opt-in.

| ID | Name | Latest (registry) | Versions in `version_history/` (resources) | LwM2M | Inst. | Zephyr file → object version shipped |
|---|---|---|---|---|---|---|
| 0 | LwM2M Security | 1.2 (31 res) | 0-1_0 (13), 0-1_1 (18), 0-1_2 (31) | 1.1 | Multi, Mandatory | `lwm2m_obj_security.c` → 1.0, or 1.1 if `CONFIG_LWM2M_SECURITY_OBJECT_VERSION_1_1` (default with LwM2M 1.1) |
| 1 | LwM2M Server | 1.2 (28) | 1-1_0 (9), 1-1_1 (24), 1-1_2 (28) | 1.2 | Multi, Mandatory | `lwm2m_obj_server.c` → 1.0, or 1.1 (`CONFIG_LWM2M_SERVER_OBJECT_VERSION_1_1`, default with 1.1) |
| 2 | LwM2M Access Control | 1.1 (4) | 2-1_0 (4), 2-1_1 (4) | 1.0 | Multi, Optional | `lwm2m_obj_access_control.c` → 1.0 |
| 3 | Device | 1.3 (23) | 3-1_0..3-1_3 (23 each) | 1.1 | Single, Mandatory | `lwm2m_obj_device.c` → 1.0 |
| 4 | Connectivity Monitoring | 1.3 (14) | 4-1_0 (11), 4-1_1 (12), 4-1_2 (13), 4-1_3 (14) | 1.1 | Single, Optional | `lwm2m_obj_connmon.c` → 1.0 (LwM2M 1.0 default), 1.2 (LwM2M 1.1 default), or 1.3 |
| 5 | Firmware Update | 1.2 (14) | 5-1_0 (9), 5-1_1 (13), 5-1_2 (14) | 1.1 | Single, Optional | `lwm2m_obj_firmware.c` (+ `lwm2m_obj_firmware_pull.c` for Package URI pull, optional CoAP proxy) → 1.0 |
| 6 | Location | 1.0 (7) | 6-1_0 | 1.0 | Single, Optional | `lwm2m_obj_location.c` → 1.0 |
| 7 | Connectivity Statistics | 1.0 (9) | 7-1_0 | 1.0 | Single, Optional | not implemented (ID macro only in `include/zephyr/net/lwm2m.h`) |
| 9 | LwM2M Software Management | 1.1 (23) | 9-1_0 (19), 9-1_1 (23) | 1.1 | Multi, Optional | `lwm2m_obj_swmgmt.c` → 1.0 |
| 10 | LwM2M Cellular Connectivity | 1.1 (14) | 10-1_0 (12), 10-1_1 (14) | 1.1 | Single, Optional | no |
| 11 | LwM2M APN Connection Profile | 1.1 (34) | 11-1_0, 11-1_1 | 1.1 | Multi, Optional | no |
| 14 | LwM2M Software Component | 1.0 (6) | 14-1_0 | 1.0 | Multi | no |
| 15 | DevCapMgmt | 2.0 (8) | 15-1_0, 15-2_0 | 1.0 | Multi | no |
| 16 | Portfolio | 1.0 (4) | 16-1_0 | 1.0 | Multi | `lwm2m_obj_portfolio.c` → 1.0 |
| 19 | BinaryAppDataContainer | 1.0 (6) | 19-1_0 | 1.0 | Multi | `lwm2m_obj_binaryappdata.c` → 1.0 |
| 20 | Event Log | 3.1 (9) | 20-1_0 (6), 20-2_0, 20-2_1, 20-3_0, 20-3_1 | 1.1 | Multi | `lwm2m_obj_event_log.c` → 1.0 |
| 21 | LwM2M OSCORE | 2.0 (7) | 21-1_0 (6), 21-1_1, 21-2_0 (7) | 1.1 | Multi | not implemented (ID macro only) |
| 22 | Virtual Observe Notify | 1.1 (7) | 22-1_0, 22-1_1 | 1.1 | Multi | no |
| 23 | LwM2M COSE | 1.0 (3) | 23-1_0 | 1.2 | Multi | no |
| 24 | MQTT Server | 1.0 (9) | 24-1_0 | 1.2 | Multi | no |
| 25 | LwM2M Gateway | 2.0 (3) | 25-1_0, 25-2_0 (3) | 1.1 | Multi | `lwm2m_obj_gateway.c` → 2.0 |
| 26 | LwM2M Gateway Routing | 1.0 (2) | 26-1_0 | 1.2 | Multi | no |
| 27 | 5GNR Connectivity | 1.0 (21) | 27-1_0 | 1.2 | Multi | no |
| 28 | Device RF Capabilities | 1.0 (8) | 28-1_0 | 1.1 | Single | no |

IPSO and uCIFI objects shipped by Zephyr (`ipso_*.c`, `ucifi_*.c`). Zephyr ships version 1.1 where marked "1.0/1.1", gated by `LWM2M_VERSION_1_1`.

| ID | Name | Registry latest (res) | Zephyr file → ver |
|---|---|---|---|
| 3300 | Generic Sensor | 1.1 (13) | `ipso_generic_sensor.c` → 1.0/1.1 |
| 3303 | Temperature | 1.1 (12) | `ipso_temp_sensor.c` → 1.0/1.1 |
| 3304 | Humidity | 1.1 (12) | `ipso_humidity_sensor.c` → 1.0/1.1 |
| 3311 | Light Control | 1.0 (8) | `ipso_light_control.c` → 1.0 |
| 3313 | Accelerometer | 1.1 (11) | `ipso_accelerometer.c` → 1.0/1.1 |
| 3314 | Magnetometer | 1.1 (10) | `ipso_magnetometer.c` → 1.0 |
| 3316 | Voltage | 1.1 (13) | `ipso_voltage_sensor.c` → 1.0/1.1 |
| 3317 | Current | 1.1 (13) | `ipso_current_sensor.c` → 1.0/1.1 |
| 3323 | Pressure | 1.1 (13) | `ipso_pressure_sensor.c` → 1.0/1.1 |
| 3333 | Time | 1.1 (5) | `ipso_time.c` → 1.0 |
| 3338 | Buzzer | 1.1 (7) | `ipso_buzzer.c` → 1.0/1.1 |
| 3340 | Timer | 1.0 (11) | `ipso_timer.c` → 1.0 |
| 3342 | On/Off switch | 1.1 (7) | `ipso_onoff_switch.c` → 1.0/1.1 |
| 3347 | Push button | 1.1 (5) | `ipso_push_button.c` → 1.0/1.1 |
| 3411 | Battery (uCIFI) | 1.1 (16) | `ucifi_battery.c` → 1.0 |
| 3412 | LPWAN Communication (uCIFI) | 1.1 (29) | `ucifi_lpwan.c` → 1.0 |
| 3435 | Filling level | 2.0 (27) | `ipso_filling_sensor.c` → 1.0 |

Consequences for the server's object model:

1. Ship every `version_history/` file, not only the latest. Zephyr registers Device 1.0, Firmware 1.0 and Security/Server 1.0/1.1 while the registry root holds 1.3/1.2.
2. Resolve each object's version from `ver=` or from the client's `lwm2m=` (VER-01).
3. Treat 7 and 21 as objects Zephyr never registers.

---

## 5. Gaps / not verified

- 1.2 (20201110) and 1.2.1 (20221209) TS PDFs were not text-extracted. 1.2 deltas in §3 come from 1.2.2.
- ETS 1.2.1-C was not opened.
- The OMA releases web page (JS-rendered) was unreadable. Currency was determined from the `/release/LightweightM2M/` directory listing.
- TestFest/SVE event-specific test plans: none found publicly beyond `ETS/`.
- The RFC section titles and values above were checked against rfc-editor.org text (7252, 7641, 7959, 8323, 6690, 8428, 6347, 9146, 7925, 4279, 6655, 5487, 7251, 5289, 7250, 8613, 9147, 8132, 9175). RFC 8790 was not downloaded; its content-format numbers come from C 1.2.2 Table 7.5-3.
