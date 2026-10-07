# Roadmap

Where the project stands and what is left to make it a feature-complete, production-grade LwM2M Server and Bootstrap-Server.

## Where we are

- **Spec coverage:** 273 requirement rows from OMA LwM2M 1.0.2, 1.1.1 and 1.2.2 (Core and Transport) in `spec/`. 265 are proven by tests (`// Proves:`, enforced by `TestSpecCoverage`), 4 are informative (no implementable behaviour, reason in `spec/coverage-informative.txt`), and 4 are pending on upstream pion/dtls (`spec/coverage-pending.txt`). Every proving test was audited with mutation checks.
- **Interop:**
  - The Zephyr LwM2M interop suite passes 66/66, unchanged, against `lwm2md`.
  - Anjay 3.15, Anjay Lite and Wakaama pass in CI (`interop/peers`).
- **Transports:** CoAP over UDP, DTLS 1.2/1.3 (PSK, X.509, CID), TCP/TLS (incl. TLS 1.3) and WebSockets; MQTT, HTTP, SMS (incl. Secured mode), NIDD and LoRaWAN.
- **Security:** OSCORE (with Echo and Appendix B.2), EST, COSE.
- **Other:** gateway objects, access control, FOTA, a Leshan-compatible REST API.
- **Dependencies:** upstream `pion/dtls/v4` (release candidate), no forks.

Priorities: **P0** blocks feature-completeness or correctness; **P1** needed for production use; **P2** depth, polish, wider interop.

---

## 1. Upstream: close the four pending requirements (P0)

| Item | Unblocks | Status | Next step |
|---|---|---|---|
| RFC 7250 raw public keys in pion/dtls | SEC-03, SEC-09, BS-10 (RPK mode) | Draft for review: fiumaralabs/dtls#1 (branch `rfc7250-raw-public-keys`) | Review, rebase on pion main, discuss API shape with maintainers, submit. Then re-enable RPK (`server.ErrRPKUnsupported` goes away), restore the RPK tests and OpenSSL RPK interop |
| `unknown_psk_identity` (115) alert | SEC-12 | Draft for review: fiumaralabs/dtls#2 (branch `unknown-psk-identity-alert`) | Same; then `TestDTLSAlerts` asserts 115 |
| DTLS 1.3 external PSK and PSK with PFS | PSK clients over DTLS 1.3; TLS13-05 features beyond certificates | Merged on pion main after v4.0.0-rc.3 | Move to the next pion v4 release; extend `SupportedTLS13Features` and add tests |
| pion/dtls v4 final release | Stable dependency (we are on an rc) | Upstream | Move when tagged |
| go-coap on pion/dtls v4 | Drop the indirect pion/dtls v3 dependency and parts of `internal/dtlscoap` | go-coap v3 depends on pion v3 | Propose a go-coap PR (or follow theirs) |
| Optional: our other two pion patches | Upstream value only; we re-implemented both in our code | Branches `resume-keeps-psk-identity`, `new-handshake-same-address` in the fork | Offer upstream after #1/#2; #253 (similar to the port-reuse fix) was declined before |

Pion's contribution policy applies: the submitter reviews and explains the code and writes PR descriptions in their own words. Reviewer notes are in `interop/upstream-pion.md`.

## 2. Validation on real devices and wider interop (P0/P1)

- **P0: real devices on cellular.** Follow `interop/REAL-DEVICE.md` (nRF9160/nRF9151 with modem-offloaded DTLS, and a Zephyr+mbedTLS board). It checks CID, surviving a NAT rebind without a new handshake, session resumption, queue-mode wake and firmware pull over Block2. Check the NCS Kconfig names against the SDK version you use, then turn the results into a recurring test.
- **P1: full OMA ETS automation.** `spec/ets-1.2.md` lists 155 ETS INT 1.2 cases; the Zephyr suite automates 60. Script the rest with `testclient` as the peer, starting with the 1.2-only cases (Profile ID int-110/111, Bootstrap-Pack int-11/12, attributes in Observe int-312/313, Discover depth int-264/266) and the multi-server cases. Needs ETS fixtures: a test CA, DNS names and a second server instance.
- **P1: interop for transports with no open-source peer.** OSCORE, EST, DTLS 1.3, MQTT, HTTP, SMS, NIDD, LoRaWAN and the gateway objects are proven only against our own `testclient`. Track commercial or new clients (Anjay commercial OSCORE/EST, modem LwM2M clients, LoRaWAN network servers) and run them when available, for example at an OMA TestFest.
- **P2: peer gaps.**
  - Anjay Lite X.509, CID and Execute are not run yet.
  - Wakaama's LwM2M 1.0 example fails to compile at the pinned commit; 1.0 is covered by Anjay `-V 1.0` instead.
- **P2: Zephyr interop on the local colima VM is flaky under load** (CI is 66/66). Make local runs robust by giving the VM more CPU or pinning the DUT.

## 3. Feature gaps and known limits (P1)

**Security and transports**
- `wss://`: use the TLS client certificate from the HTTP request as the X.509 identity (it is NoSec plus address today).
- TLS over TCP supports certificates only, because Go's crypto/tls has no TLS-PSK or raw public keys. Options: wait for Go, or add a pluggable TLS stack.
- `server.Identity` has no OSCORE mode or broker-authenticated (MQTT) mode. OSCORE peers are carried as NoSec plus a synthetic address (`transport/coap/oscore.go:56`), and MQTT endpoints that have credentials are refused by SEC-07. Add identity modes for OSCORE and for lower-layer or broker authentication, and add OSCORE fields to `SecurityInfo` (`leshanapi/json.go:538`).
- The BS-10 "low-entropy PSK" check only checks a minimum length of 16 bytes. Add an entropy estimate, or refuse known weak or password-like keys.
- MQTT COSE has random IVs and no replay window (`transport/mqtt/cose.go:62`), because T §8 defines neither. Add replay protection and record the ambiguity.
- SMS Secured-mode counters are in memory (`transport/sms/securedpacket.go:175`). Persist them.
- Bootstrap `ListenTLS` does not apply the default port (the server's does, TCP-02).

**Device management and data model**
- Gateway end devices: Discover, Write, Execute, Create, Delete and composite operations go only through `gateway.Client`. Read and Observe already take `Prefix` on the server API; add it to the rest.
- Create without an instance ID (the client assigns it, DM-09): add it to the server API. `leshanapi` currently picks the lowest free ID, which races a concurrent create (`leshanapi/leshanapi.go:513`).
- A raw `Write` to `/1/x/1` does not update the stored lifetime (only `SetLifetime` does). Decide whether Write should mirror it.
- A forced `Format` on Write, Create and WriteComposite skips the DM-05 and ETCH-03 format checks. Validate forced formats too.
- Block size: one server-wide `BlockSZX` (512 bytes). Make it configurable per endpoint, and honour a client's 4.13 asking for smaller blocks (go-coap ignores it today).
- Block1 reassembly and WebSocket messages are capped at 1 MiB (`transport/coap/block1.go:22`, `internal/coapws/coapws.go:24`). Make the caps configurable.
- `model`: only `a..b` ranges and quoted enumerations are enforced. The registry's free-text ranges ("0-999", "8 bit", prose) are not (`model/schema.go:80`).
- `lwm2m.ParsePath` rejects ID 65535, so codecs carry workarounds for the Create-without-instance case (`codec/omajson/omajson.go:236`).
- SenML: an explicit `"bt":0` cannot reset the base time (`codec/senml/senml.go:127`).
- OBS-04: the client restarting its notification timer cannot be seen from the server. It stays partly proven by design.

**Bootstrap**
- When a client rejects Bootstrap-Finish with 4.06, the server reports it but does not correct the config and retry.
- LwM2M 1.0-style server-initiated bootstrap (Bootstrap-Server writes without a `/bs` request) is not implemented.
- The SMS resources of `/0` (6-9, 13, 15, 16) are not modelled (`bootstrap/config.go:103`).
- Config validation does not check the C bit on create-rights ACLs (BS-25), or that ACL owners refer to provisioned server accounts (BS-26).
- Bootstrap-Write over HTTP writes whole instances; Tbl 7.1.2-1 shows per-resource writes.

**EST and Leshan API**
- EST: only the default `/.well-known/est` routes are served (no ArbitraryLabel routing, no `/.well-known/core` discovery).
- Leshan API:
  - `GET /bootstrap` (list all) is missing.
  - Registration `objectLinks` are rebuilt from the parsed list instead of echoed (`leshanapi/json.go:467`).
  - Deleting security info does not evict the registration (use `Server.RevokeSecurity`, `leshanapi/leshanapi.go:669`).
  - No server RPK or certificate is exposed (`leshanapi/leshanapi.go:649`).
  - The SLEEPING, COAPLOG and REQUEST_RESPONSE events are not emitted.

## 4. Production readiness (P1)

- **Persistence.** Registrations, observations, queued requests, DTLS sessions, OSCORE contexts and counters live in memory only. Add pluggable stores (`server.Store`, `SecurityStore`, a session store) with a durable implementation, for example SQL or Redis.
- **Scale-out.** Routing by Connection ID across nodes, sharing DTLS session state, and node restarts without forcing every device to re-handshake (the original M10 milestone). Also leader-free expiry, and fan-out of observations and events.
- **Native northbound API.** A typed API beyond the Leshan-compatible one:
  - async, queued commands with results;
  - a durable event stream for registration, notification and Send events (webhooks or a message bus);
  - multi-tenancy and authentication/authorisation (the Leshan API has no auth today).
- **Operations.** Metrics (Prometheus: registrations, handshakes, retransmissions, queue depth), structured logs, tracing, health and readiness endpoints, a config file for `lwm2md` (flags only today), a container image, and release tooling (tagged releases, changelog, semver; we are pre-v0.1).
- **Hardening.**
  - Fuzz every codec and parser (TLV, SenML, LwM2M CBOR, link-format, attributes, CoAP framing, SMS packets).
  - Run load and soak tests: 100k+ registrations, sleepy-fleet wake storms, NAT-rebind storms.
  - Get an external security review of the DTLS demux, OSCORE, SMS Secured mode and the bootstrap paths.
  - The nightly `-race` stress job guards against the one race seen once before.
- **Resource bounds.** Evict ended registrations from the schema cache (`server/model.go:46`). Expire incomplete SMS concatenation sets (`transport/sms/sms.go:249`). Clean up superseded OSCORE peers (`transport/coap/oscore.go:360`). Index observations and SMS numbers instead of linear scans (`transport/coap/oscore.go:318`, `transport/sms/sms.go:383`). Bound the event buffers (`leshanapi/events.go:39`).

## 5. Spec work (P2)

- Record, as A-entries in `spec/standards-1.2.md`, the gaps implementations had to fill:
  - COSE plaintext and AAD in MQTT Outer_Wrapper;
  - `/23` keys typed String;
  - `pct` used for MQTT Bootstrap-Read and Pack;
  - the `/0/x/7` length for SMS keys;
  - the Execute argument for the binding override;
  - DTLS 1.3 feature bits.
- Track OMA LwM2M releases after 1.2.2 and new ETS versions, and re-run the requirement extraction for each.
- Report the TS and ETS errata found (malformed TLV examples, the MQTT Send opcode, the stale HTTP Bootstrap-Read table, about 25 ETS errors) to OMA.

## 6. Housekeeping (P2)

- Merge the AEAD algorithm tables of `security/cose` and `security/oscore` (`security/cose/cose.go:41`).
- `testclient`: the TCP client's notify path, and the firmware worker's lifetime (`testclient/tcpclient.go:26`, `testclient/firmware.go:93`).
- Keep `doc/ARCHITECTURE.md`, `README.md` and the package docs in step with each item above.
