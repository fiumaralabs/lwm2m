# pion/dtls v3.1.10, patched for LwM2M

This directory holds github.com/pion/dtls/v3 v3.1.10 (MIT, see `LICENSE`, copyright the Pion community), minus tests, examples and CI files. The root `go.mod` points to it with a `replace` directive, so go-coap and the server keep using the `github.com/pion/dtls/v3` import path.
`LWM2M.patch` is the full diff against upstream. Every changed line is marked `lwm2m patch`.

## Why a patch

pion has no RFC 7250 raw public keys, which security mode 1 (RPK) needs (T §5.2.9.2, SEC-09; the Bootstrap-Server MUST support it, BS-10). RPK changes the handshake itself: the certificate-type extensions and the form of the Certificate message, which is a bare `SubjectPublicKeyInfo<1..2^24-1>` instead of a `certificate_list`. Both ends hash these messages into Finished and CertificateVerify, so a record-layer shim cannot rewrite them. pion's hooks only cover Hello and CertificateRequest.

We ruled out the alternatives:

- A cgo binding to mbedTLS (or OpenSSL) would replace the whole DTLS stack under go-coap, and go-coap's DTLS server builds on pion. It would also break pure-Go cross-compilation.
- A renamed fork under `internal/` would split the type space (go-coap takes `*dtls.Config` and the server inspects `*dtls.Conn`).

The `replace` keeps the patch small and in one place, and it does nothing unless enabled: pion's whole upstream test suite passes on the patched tree.

## Changes

1. **RFC 7250 raw public keys.**
   - `Config.ClientCertificateTypes` and `Config.ServerCertificateTypes` hold the types in preference order. nil means X.509 only and sends no extension.
   - The `client_certificate_type` (19) and `server_certificate_type` (20) extensions are added to `pkg/protocol/extension`.
   - The server selects the types in `negotiateCertificateTypes` and answers `unsupported_certificate` (43) when there is no common type.
   - The client checks the ServerHello's selections against what it offered.
   - `MessageCertificate.RawPublicKey` selects the RFC 7250 §3 wire form. The handshake rejects a Certificate whose form differs from the negotiated type.
   - A raw key's credential is the SPKI of `Certificates[0].PrivateKey`. `VerifyPeerCertificate` receives `[]{SPKI}` and makes the trust decision, since a raw key has no chain. Without it a raw key is refused unless the config opts out (`InsecureSkipVerify` on a client, `ClientAuth` below `VerifyClientCertIfGiven` on a server).
   - CA names from a CertificateRequest do not filter a raw key.
2. **`unknown_psk_identity` (115)** is the alert for an unknown PSK identity (RFC 4279 §2) in place of `internal_error` (80). LwM2M clients class 115 as "Fail" (T Tbl 5.2.10-1). Table 5.2.10-1 does not list 80 at all.
3. **The PSK identity is kept in `Session.IdentityHint`**, so a resumed session keeps its authenticated identity (SEC-11, README C2).
4. **ECDHE curve selection** takes the client's most preferred curve that we support (RFC 8422 §5.1, the /0/x/18 order). Upstream took the first offered curve, even an unsupported one.
5. **A new handshake from an address that already has a session** (RFC 6347 §4.2.8, `internal/net/udp`). Upstream routed every datagram from a known address to the existing connection, so an epoch-0 ClientHello from a client that reuses its port was swallowed and the new handshake hung. Anjay reuses its last local port after bootstrap, a re-Register or a restart, and NATs do the same. Now such a ClientHello starts a pending connection. Epoch-0 records go only to the pending connection, and later epochs go to both (each drops what it cannot decrypt). The pending connection takes over the address only once its handshake completes (`HandshakeDone`, called from `conn.go`), so a spoofed ClientHello cannot steal an established session. `PacketConn.Close` deletes only map entries it still owns. Found by `interop/peers` and pinned by `server.TestDTLSNewHandshakeFromSamePort`.

## Upgrading

Copy the new upstream release here, apply `LWM2M.patch` (`patch -p1`) and drop the tests. Then run upstream's own suite once on the patched tree, and `go test ./server ./dtlssuite` (including `TestOpenSSLInterop`, which runs RPK against OpenSSL 3.2+).
