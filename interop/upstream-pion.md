# Upstream pion/dtls branches: reviewer notes

These notes are for the maintainer who will submit the patches, not PR text. pion's AI policy (webrtc wiki, Contributing) asks for PR descriptions in your own words and for you to be able to explain the code.

- Every branch is in github.com/fiumaralabs/dtls, based on pion/dtls `main` at `bc08aaa` (module `/v4`, after the DTLS 1.3 refactor). Each branch is one commit, and none has an open PR.
- On each branch, all of these pass:
  - `golangci-lint run` (v2.10.1, the repo's config)
  - `go test -race ./...`
  - the current .goassets checks: `lint_commit_message.go`, `lint_filename.go`, `lint_no_trailing_newline_in_log_messages.go` and `lint-go-mod-version.sh`
- The scripts the wiki names (`lint-commit-message.sh`, `assert-contributors.sh`, `lint-disallowed-functions-in-library.sh`) are gone from .goassets. The Go checks above replaced them in CI.
- The REUSE job was not run locally. New files carry SPDX headers.
- Each commit ends with a `Co-Authored-By: Claude` trailer. Keep or drop it as you see fit before submitting.
- The wiki asks you to talk to a maintainer (Discord, or an issue) before you start work. That matters most for RPK (a feature) and port reuse (#253 was declined).
- The pion/dtls README says DTLS 1.2 fixes and improvements target the `v3` branch. Every patch here is mostly 1.2, so ask whether they want main, v3, or both. pion/webrtc and our own fork are on v3.

| Patch | Branch | Commit | Status |
|---|---|---|---|
| 1 RFC 7250 raw public keys | `rfc7250-raw-public-keys` | 0a4943c | ready, feature: ask maintainers first |
| 2 alert 115 unknown_psk_identity | `unknown-psk-identity-alert` | 2f94f80 | ready |
| 3 resumed session keeps PSK identity | `resume-keeps-psk-identity` | 2f84593 | ready |
| 4 client-preferred ECDHE curve | none | | already on main; v3 backport is open PR #1153 |
| 5 new handshake from a port with a live association | `new-handshake-same-address` | 257558c | ready; #253 was "won't implement" |

## Main is v4: what it means for us

- pion/dtls `main` is module `github.com/pion/dtls/v4` (tags `v4.0.0-rc.1` to `rc.3`, no stable release yet), so every branch here targets v4. Our fork, `lwm2m-v3` / `v3.1.11-lwm2m.1`, is v3, and so are pion/webrtc and go-coap (go-coap `main` still requires `pion/dtls/v3`).
- If the patches land only in v4, we drop the fork by moving to `pion/dtls/v4`:
  - **No `Config` struct in v4.** `dtls.Client`, `Server` and `Listen` take functional options (`ClientOption`, `ServerOption`). `transport/coap/dtls_config.go`, `transport/coap/coap.go`/`bootstrap/server.go` (`DTLSConfig.Config *piondtls.Config` is public API), `testclient` and the tests all build `*piondtls.Config` today, so this is an API change for our users too.
  - **`Listen` changed.** It now takes a `net.PacketConn` (`ListenAddr` for an address). `ConnectionState` and `SessionStore` still exist, so `transport/coap/coap.go` (`IdentityOf`) and `transport/coap/dtls_session.go` mostly carry over.
  - **go-coap needs no fork.** `internal/dtlscoap` only uses go-coap's exported session and conn constructors, so it can wrap `pion/dtls/v4` the same way it wraps the fork today. Only that package's imports and `Listen`/`Dial` change.
  - **RPK is 1.2-only on the v4 branch.** LwM2M 1.2 uses DTLS 1.2, so that is enough for us.
- If maintainers also take v3 backports (their README says DTLS 1.2 fixes go to `v3`), we can go back to upstream `pion/dtls/v3` with nothing to rewrite beyond the import path. That is the cheaper path for us, so ask for it.
- Either way, the fork's patch 5 should first get the pending-slot fix from the v4 branch (see Patch 5, "Bug found while porting").

## Patch 1: `rfc7250-raw-public-keys` (fiumaralabs/dtls, based on pion/dtls main bc08aaa, commit 0a4943c)

**What it does**
- Adds RFC 7250 raw public keys to the DTLS 1.2 handshake on main (module `github.com/pion/dtls/v4`).
- New extension payloads in `pkg/protocol/extension/certificate_type.go`. They follow main's Offer/Selection convention: `ClientCertificateTypeOffer`/`Selection` and `ServerCertificateTypeOffer`/`Selection`, plus `TypeClientCertificateType` (19) and `TypeServerCertificateType` (20). They are registered for ClientHello and the DTLS 1.2 ServerHello only.
- `handshake.MessageCertificate.RawPublicKey`: the RFC 7250 §3 body is a single `ASN.1_subjectPublicKeyInfo<1..2^24-1>`. On unmarshal the form is detected by the 0x30 SEQUENCE tag at byte 3. In a certificate_list that byte would mean a first entry of at least 3 MiB, larger than the 2 MB fragment buffer accepts.
- Negotiation lives in `internal/flight/flight12/certificate_type.go`:
  - The client offers its configured lists (flight1 and flight3).
  - The server selects the first of its own types that the client offered and echoes the selection only when the client offered the extension. It sends `unsupported_certificate` when there is no common type, or when the extension is absent but its list excludes X.509.
  - The client checks the ServerHello selections against what it offered.
  - Both sides reject a Certificate message whose form differs from the negotiated type.
- The SPKI sent is derived from `Certificates[0].PrivateKey`. The certificate bytes of an RPK credential are never sent but must still be non-empty, because `validateConfig` requires them.
- Trust:
  - `VerifyPeerCertificate` receives `[][]byte{SPKI}` with nil chains.
  - A client without the callback refuses the key (`ErrUnverifiedRawPublicKey`) unless `InsecureSkipVerify` is set.
  - A server treats an RPK client as verified only if a callback is set. So `RequireAndVerifyClientCert` without a callback fails, and `RequireAnyClientCert` accepts the key.
- CA names from a CertificateRequest do not filter an RPK client credential.
- `handshakecrypto.verifyCertificateSignature` falls back to `x509.ParsePKIXPublicKey` when the peer credential is not a certificate.
- Default behaviour is unchanged: with neither option set nothing is offered, and a server ignores the extensions (allowed by RFC 7250 for a server that does not support them).

**RFC sections**
- RFC 7250 §3: the extension formats and the Certificate structure.
- RFC 7250 §4.1 and §4.2: client offer, and server selection with `unsupported_certificate`.
- RFC 7250 §4.3: the client's Certificate.
- RFC 8446 §4.4.2 (CertificateEntry for 1.3): not implemented, see open questions.

**API added**
- `dtls.CertificateType`, `dtls.CertificateTypeX509`, `dtls.CertificateTypeRawPublicKey` (aliases of the extension package type).
- `WithClientCertificateTypes(...CertificateType) Option`
- `WithServerCertificateTypes(...CertificateType) Option`
- Errors (internal): `ErrEmptyCertificateTypes`, `ErrUnsupportedCertificateType`, `ErrUnverifiedRawPublicKey`, `ErrCertificateTypesRequireDTLS12`.
- `extension.CertificateType`, the four payload types, and `MessageCertificate.RawPublicKey`.

**How it was tested**
- `go test -race ./...`: all packages pass.
- `golangci-lint run` (v2.10.1, repo config): 0 issues.
- goassets scripts:
  - `lint_commit_message.go`, `lint_filename.go`, `lint_no_trailing_newline_in_log_messages.go` and `lint-go-mod-version.sh` all pass.
  - SPDX headers are on all four new files. The `reuse` tool is not installed, so the REUSE job itself was not run.
- New tests:
  - `TestRawPublicKeyHandshake`: server RPK; mutual RPK; RPK client against an X.509 server; no common type → `unsupported_certificate`; a server without configured types ignores offers; an RPK-only client rejects an X.509 server; unverified key; key rejected by the callback → `bad_certificate`.
  - `TestCertificateTypesOptions`: empty list, unknown type, DTLS 1.3 enabled.
  - Extension codec tests and `FuzzCertificateTypeUnmarshal` (matching the existing fuzz pattern).
  - `TestHandshakeMessageCertificateRawPublicKey`, and an updated `TestExtensionContextRegistry`.
- Manual interop with OpenSSL 3.6.4 (scratch program, not committed):
  - pion server with mutual RPK ↔ `openssl s_client -dtls1_2 -enable_client_rpk -enable_server_rpk`: data echoed.
  - pion client ↔ `openssl s_server -enable_server_rpk`: handshake OK, peer SPKI is 91 bytes (P-256).
- The same logic has run in fiumaralabs/lwm2m on v3 (fork tag v3.1.11-lwm2m.1) against Anjay, Wakaama, Zephyr and OpenSSL.

**Open questions for maintainers**
- Per the wiki, ask on Discord or in an issue before opening a PR. This is a feature, not a fix.
- **Scope**: is this wanted on `main` only, or also on `v3`? pion/webrtc still uses v3, and our real need is on v3.
- **DTLS 1.3**: RFC 7250 also applies there (extensions in EncryptedExtensions, `CertificateEntry` carrying the SPKI). The branch rejects the options when MaxVersion is 1.3. Do they want 1.3 in the same PR or a follow-up?
- **API shape**:
  - Two options, or one `WithCertificateTypes(client, server)`?
  - Should the credential be an explicit raw-key option (e.g. `WithRawPublicKey(crypto.Signer)`) instead of reusing `Certificates[0].PrivateKey` with ignored certificate bytes?
- **Certificate selection**: `GetCertificate`/`GetClientCertificate` don't know the negotiated type. Should `ClientHelloInfo`/`CertificateRequestInfo` expose it?
- **Decoding**: is the 0x30 heuristic in `MessageCertificate.Unmarshal` acceptable, or would they rather pass the negotiated type as decode context (the way `KeyExchangeAlgorithm` reaches ServerKeyExchange/ClientKeyExchange)? That would mean plumbing through the flight cache.
- **VerifyPeerCertificate semantics**: is a nil-chain call with the SPKI fine? Or should there be a dedicated `VerifyPeerRawPublicKey` callback, and should `State` expose the negotiated type?
- **Resumption**: there is no special handling. A server already disables resumption when the client sends a certificate. A server-RPK session resumes like an X.509 one.

**Related upstream issues/PRs**
- None found for RFC 7250, raw public keys or `client_certificate_type` (searched `gh search issues --repo pion/dtls` and org-wide). #356 matched by keyword only and is unrelated.
- The #1009 extension refactor on main is the base this builds on.

## Patch 2: `unknown-psk-identity-alert` (fiumaralabs/dtls, 2f94f80, on pion main bc08aaa)

- What it does: adds `alert.UnknownPSKIdentity` (115) and its `String()`. The DTLS 1.2 server sends it when the PSK server callback returns `nil, nil` ("none match" in the documented `PSKServerCallback` contract) for the client's identity. Before, main sent `handshake_failure` (40) for this case. v3 sent `internal_error` (80), because the v3 API only had "return an error". A callback that returns an error still gets `internal_error`, so only "identity not known" changes.
- Diff: 3 lines in `pkg/protocol/alert/alert.go`, 1 line plus an RFC link in `internal/flight/flight12/flight4handler.go`.
- RFC: RFC 4279 §2 ("If the server does not recognize the PSK identity, it MAY respond with an unknown_psk_identity alert message"). The value 115 comes from RFC 4279 §2 / the IANA TLS Alert registry. Why LwM2M needs it: OMA LwM2M Transport §5.2.10, Table 5.2.10-1, classes 115 as a client-side "Fail", and the table has no row for 40 or 80.
- Tests: `TestUnknownPSKIdentityAlert` (conn_test.go) runs a full 1.2 handshake over `handshakePair`. The server knows no identity, and the client's handshake error must carry the fatal 115 alert. I checked that it fails on unpatched main ("in chain: alert: Alert Fatal: HandshakeFailure"). `TestUnknownPSKIdentityDescription` pins the value and the name.
- Checks: golangci-lint v2.10.1 found 0 issues. `go test -race ./...` passes. lint_commit_message, lint_filename, lint_no_trailing_newline_in_log_messages and lint-go-mod-version all pass. The test callback needed `//nolint:nilnil`, because `nil, nil` is the documented "no match" return (upstream already suppresses nilnil in flight3/4).
- Open questions for maintainers:
  - Should 115 also cover a callback that returns an error? I kept errors on `internal_error`, because an error can mean a DB outage and not an unknown identity. Would a sentinel error (for example `ErrUnknownPSKIdentity` that callbacks may return) suit them better?
  - Should DTLS 1.3 change too? RFC 8446 §6.2 makes unknown_psk_identity OPTIONAL and allows decrypt_error. Main's 1.3 server falls back to certificates, or sends `handshake_failure` with `ErrPSKNotNegotiated` (`internal/handshake/psk_handshake.go` pskFallback, `flight13/flight3handler.go`). I left 1.3 alone.
  - The returned error is still `ErrPSKNotNegotiated`, whose text says "DTLS 1.3 external PSK was not negotiated", although this is the 1.2 path. It predates this change. Should it get its own error?
  - Backport: the v3 README says DTLS 1.2 fixes target the `v3` branch. On v3 the change would cover the error from `Config.PSK`, since there is no nil-means-unknown contract (our fork does that). Do they want a v3 PR as well?
- Related upstream: none found (I searched issues/PRs for "unknown_psk_identity", "UnknownPSKIdentity", "115", "PSK identity"). Nearby work: #1165 "Rework WithPSK for 1.3, identities and hashes", which brought the nil-PSK contract, and #1171/#1172 on the empty ECDHE-PSK identity hint.

## Patch 3: `resume-keeps-psk-identity` (fiumaralabs/dtls, 2f84593, on pion main bc08aaa)

- I verified main has the bug before fixing it. `TestResumedPSKSessionKeepsIdentity` (a 1.2 PSK handshake with a SessionStore on both sides, then a second handshake that resumes the same session ID) failed on unpatched main: the server's `ConnectionState().IdentityHint` was nil after resumption. An abbreviated handshake has no ClientKeyExchange, and `Session` stored only ID and master secret.
- What it does: adds `Session.IdentityHint`, and widens the internal `HandshakeConfig.GetSession`/`SetSession` to carry it. The server stores it in flight4 and restores it in `handleHelloResume`. The client stores it in flight5 and restores it in flight1. When the server declines resumption and runs a full handshake, flight3 clears it so a stale value cannot leak. 9 files, about 15 lines outside the test.
- RFC: RFC 5246 §7.3 / RFC 6347 §4.2. A resumed session inherits the original session's security parameters, and the peer identity is one of them (RFC 5246 §7.3: the session state includes the peer certificate, and for PSK suites the identity plays that role). Why LwM2M needs it: the server finds the device by PSK identity (LwM2M Transport §5.2.8, and identity binding on re-registration). Without it, a device that resumes looks unauthenticated.
- Tests: `TestResumedPSKSessionKeepsIdentity` asserts that resumption happened (same SessionID) and that the identity matches on both handshakes. golangci-lint found 0 issues, `go test -race ./...` passes, and all goassets scripts pass.
- Open questions for maintainers:
  - Public API: `Session` gets a new exported field, which is additive. Custom `SessionStore`s that serialize `Session` by hand need to persist it. Would they prefer a different name, such as `PSKIdentity`? I kept `IdentityHint` to match `State.IdentityHint`.
  - Should the server re-check the identity with the PSK callback on resumption, so a revoked identity cannot resume? The patch only restores the identity. The application can still reject the connection afterwards (for example in VerifyConnection).
  - The client-side restore is for symmetry only. On a client, `IdentityHint` is the server's hint, usually empty. Should it be dropped to keep the diff smaller?
  - DTLS 1.3: `flight13/flight3handler.go` sets `IdentityHint` to `psk.Identity`. For ticket resumption that is the ticket identity, not the external PSK identity of the original connection. It looks like the same class of problem, but I did not touch it.
  - Backport to `v3` (the README says 1.2 fixes go there): our fork carries the v3 version of this.
- Related upstream: none found for "IdentityHint", "resumed identity" or "session resumption PSK". Context: #369 "Stateful session resumption" (the original feature), #335 "Add PSK Client Hint to conn state" (added `IdentityHint` to the state), and #447 (closed: resumption with client certificates, the same "identity after resume" question for certificates).

## Patch 4: client-preferred ECDHE curve (no branch)

- **Upstream already fixed this on main, so no branch.** In `internal/flight/flight12/flight0handler.go`, `selectEllipticCurve(cfg.EllipticCurves, ext.Groups)` (`curves.go`) walks the client's list in order, takes the first curve the server is configured for, and sends `insufficient_security` when nothing matches. This is the same semantics as our v3 patch (RFC 8422 §5.1). `curves_test.go` covers it.
- v3 backport: PR #1153 "Honor configured elliptic curves on accept path" (jjinno, open) does exactly this for v3. JoTurk said he will review and merge it. If you want it on v3 sooner, comment or review there; don't open a duplicate.
- #1188 (closed as a duplicate of #1153, and flagged as AI-generated) reported the same thing.

## Patch 5: new handshake from an address that already has a session (branch `new-handshake-same-address`)

- **Branch / commit:** `fiumaralabs/dtls` `new-handshake-same-address`, one commit `257558c` on upstream `main` (`bc08aaa`, module `/v4`). Files: `internal/net/udp/packet_conn.go`, `listener.go`, `conn.go` (4 lines), `internal/net/udp/packet_conn_test.go`, new `listener_test.go`.
- **The problem, reproduced on main:** a client handshakes, drops its state without sending close_notify (reboot, or a NAT reusing the mapping), then sends a new epoch-0 ClientHello from the same IP:port. The listener's address route still points at the old `udp.PacketConn`, whose replay window drops the ClientHello ("discarded duplicated packet (epoch: 0, seq: 0)"), so the new handshake times out. The new test fails on unmodified main for 1.2, 1.2+CID and 1.3 (`context deadline exceeded` on the second dial).
- **What the change does:**
  - New udp listener option `WithNewHandshakeOnAddress(classify)`. `listener.go` always passes `classifyFirstRecord`, which uses `recordlayer` to say whether the first record is epoch 0 and, if it is a ClientHello with fragment offset 0, returns the client random.
  - When an address's conn has completed its handshake and an epoch-0 ClientHello arrives, the listener queues a new *pending* conn for Accept. The old conn keeps the address route.
  - While a pending conn exists, epoch-0 records go only to it. All other records go to both conns, and each drops what it can't authenticate (RFC 9147 §5.11 calls this "trial decryption").
  - `Conn.HandshakeContext` calls `packetConn.HandshakeComplete()` on success. For a pending conn this moves the address route to it and closes the old `udp.PacketConn`, so the old `dtls.Conn` fails on its next read or write.
  - A ClientHello with a *different* random replaces the pending conn. The replaced one is detached: its buffer is closed and its writes return EOF. A retransmitted ClientHello, or the one answering a cookie, keeps the same random (RFC 6347 §4.2.1, RFC 8446 §4.1.2), so it stays on the same pending conn.
- **Spec:** RFC 6347 §4.2.8 and RFC 9147 §5.11 (same text): the server SHOULD proceed with the new handshake, MUST NOT destroy the existing association until the client has shown it is reachable (a cookie exchange or a complete handshake with a verifiable Finished), and MUST abandon the old association after a correct Finished. We wait for the full handshake, the stricter of the two, so it also holds with `WithInsecureSkipVerifyHello`.
- **Replay protection:** unchanged. The old conn never sees epoch-0 records once a pending conn exists, and pion checks the replay window before decrypting but only marks a record as seen after it authenticates (`conn.go` `replayMarker` / `markPacketAsValid`). So copies of the new handshake's records that the old conn can't decrypt don't move its window. That answers Sean-Der's objection on #253.
- **How it was tested:**
  - `TestListenerNewHandshakeFromSameAddress` (root package; subtests 1.2, 1.2/CID, 1.3, 1.3/CID), over real loopback UDP:
    1. A client handshakes and echoes data.
    2. A spoofed ClientHello is injected from the client's own address. It is a captured first flight, all datagrams, since the 1.3 hybrid key share splits the ClientHello over two. The old session must keep working.
    3. The client socket is closed without close_notify, and the same port is rebound.
    4. A new handshake must succeed, the old server conn must be closed, and traffic must route to the new one.
  - `TestListenerNewHandshakeOnAddress` (udp package, in-memory): routing rules, retransmission keeps the same pending conn, replacement by a new random, takeover, and the previous conn closed.
  - `golangci-lint run` (v2.10.1): 0 issues. `go test -race ./...`: ok. `go test -race -count=20 ./internal/net/udp` and the new root test with `-count=20`: ok.
  - goassets `lint_commit_message.go`, `lint_filename.go`, `lint_no_trailing_newline_in_log_messages.go`, `lint-go-mod-version.sh`: all pass. The new file has SPDX headers.
  - On the v3 branch, the earlier version of this patch is what makes `server.TestDTLSNewHandshakeFromSamePort` and the Anjay interop (re-registration after bootstrap) pass in fiumaralabs/lwm2m.
- **Bug found while porting (not in our v3 patch):** in the v3 patch, a ClientHello that never completes (spoofed, or a lost attempt) held the pending slot. The real client's next ClientHello on that slot was dropped as a seq-0 replay until the pending handshake timed out. The random-keyed replacement fixes it here. The v3 fork (`lwm2m-v3`) still has the old behaviour and should get the same fix.
- **Open questions for maintainers:**
  - #253 was closed "will not implement" (Sean-Der, 2020): keep replay detection, fewer corner cases, users can pre-filter packets. This design keeps replay detection and touches only the listener demux. Are they open to it now that main has a routing layer (#1118) where it fits?
  - Default or opt-in? It is on by default for `dtls.Listen*`. The udp option exists either way, so a public `ServerOption` to turn it off would be small. Behaviour only changes for an address whose conn is already established and then receives a fresh ClientHello, which main today just drops.
  - Abandoning the old association closes its `udp.PacketConn`, so the app sees an error on the old `dtls.Conn`, with no close_notify sent to the (gone) peer. Do they want a specific error or a callback instead?
  - Pending slot: one per address, and a new random replaces it. An off-path attacker spoofing ClientHellos from the victim's address can keep replacing the pending attempt and slow a real re-handshake, but can never take over or break the established session. Acceptable, or should a pending conn that has already completed a cookie exchange be protected from replacement?
  - CID: with CID negotiated, records carrying a CID are routed by CID as before. Only address-routed records are split. Is that consistent with the #1118 rules ("drop unknown CIDs, never fall back to address")? We believe so, since we don't add any CID fallback.
  - v3 backport: pion/webrtc and most users are still on `dtls/v3`, where the demux is different (`internal/net/udp` keyed by `raddr.String()`). Would they take a v3 port (README says DTLS 1.2 fixes target `v3`)?
- **Related upstream:** #253 (same request, closed won't-implement), #254 (same root cause: server ignores the new handshake after a client/server restart, closed), #132 (restart recovery, closed), PR #1118 (CID routing rules, merged, the layer this builds on), PR #1050 (RRC path validation, which also moves address routes only after validation). No open issue or PR duplicates this.
