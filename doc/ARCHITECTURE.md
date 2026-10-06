# Architecture

The module is a library first: an LwM2M Server core that knows nothing
about transports, and adapters that connect it to CoAP, MQTT, HTTP, SMS,
NIDD and LoRaWAN. This file records the package layout and the decisions
behind it.

## Layout

```
lwm2m                 core types: paths, values, nodes, content formats
codec/...             data formats (text, opaque, TLV, CBOR, SenML, OMA JSON, LwM2M CBOR); codec/all registers them
link, attr            CoRE link-format, notification attributes
model                 OMA object registry, version resolution, validation
acl                   Access Control (/2) model

server                LwM2M Server core: Registration, Device Management, Information
                      Reporting, queue mode, Send, events; the Peer/Message API
transport/coap        CoAP bindings U and T for the Server: UDP, DTLS (PSK, RPK, X.509, CID),
                      TCP/TLS, WebSockets; the server-side OSCORE layer; Message <-> CoAP
transport/mqtt        binding M (T §8), Server and Bootstrap-Server
transport/http        binding H (T §7), Server and Bootstrap-Server
transport/sms         binding S (T §6.8.3)
transport/nidd        binding N (T §6.8.5)
transport/lorawan     LoRaWAN binding (T §6.8.4)

security/oscore       OSCORE (RFC 8613) protocol: contexts, protect/unprotect, Echo
security/est          EST over CoAPs (RFC 9148)
security/cose         COSE_Encrypt0 (RFC 9052)
security/dtls         DTLS 1.2 pieces pion lacks: CBC suite, RPK credentials, cert checks (was dtlssuite)

bootstrap             Bootstrap-Server, with its own CoAP adapter (see below)
gateway               LwM2M Gateway (/25, /26)
fota                  firmware update orchestration and block-wise file server
leshanapi             Leshan-compatible REST API (was compat)
testclient            scriptable reference client, like net/http/httptest

internal/regparam     Register/Update query parsing (was regparam)
internal/coapwire     CoAP datagrams over opaque payload transports (SMS, NIDD, LoRaWAN)
internal/coapws       CoAP over WebSockets framing
internal/dtlscoap     pion DTLS listener for go-coap
internal/vectors      golden test vectors

cmd/lwm2md            server binary
interop/...           interop suites against real clients (build tag interop)
```

Package names are the last path element (`coap`, `mqtt`, `http`, `sms`,
`nidd`, `lorawan`, `oscore`, `est`, `cose`, `dtls`), as in go-kit's
`transport/http`. Inside `transport/http` net/http is imported as
`nethttp`, and inside `transport/mqtt` the Paho client as `paho`; callers
that need both a binding and the package it shadows alias one of them.

## Decisions

**The core is binding-neutral.** `server` opens no listener and imports
neither pion/dtls nor go-coap's connection packages. It still uses go-coap's `codes.Code`
in `Message`: CoAP numbering is the vocabulary of every LwM2M binding
(MQTT and HTTP map onto it too). Every transport reaches the core through
the same exported API:

- `Server.HandleUplink(peer, msg) (resp, after)`: one uplink message; the
  transport sends `resp`, then calls `after` (GEN-10).
- `Peer`: the transport session; the core sends downlinks with
  `Peer.Exchange`.
- `Server.KnownObservation(token)`: lets a transport answer a notification
  for an unknown observation with Reset (OBS-02) before decoding it.

Splitting the CoAP adapters out needed three more, added deliberately:

- `Server.Registered(peer) bool`: whether `peer` is the current session of
  a live registration. The CoAP transport keeps such sessions open past
  go-coap's idle timeout so a DTLS session (and CID) lasts as long as the
  registration (T §5.2.8). It replaces the core's private `heldConns`,
  which type-asserted the CoAP peer.
- `Registration.Peer() Peer`: the session a registration is bound to, for
  transports and applications that need its binding or address.
- `Server.Config() Config`: the configuration in effect, defaults applied.
  The CoAP transport takes its clock (OSCORE Echo lifetime) and block-wise
  timeout from it, so tests with a fake clock keep working.

**`transport/coap` holds every go-coap adapter of the Server**, created
with `coap.New(srv)`: `ListenUDP`, `ListenDTLS`,
`DTLSConfig(CertificateModes)`, `ListenTCP`, `ListenTLS`,
`WebSocketHandler`, `EnableOSCORE` and `Close`. Its state (per-connection
peers, deferred `after`s, Block1 reassembly, held connections, the OSCORE
layer) was already separate from the core in `server/`; it moved as is.
The DTLS and TLS capability constants (`DTLSExtensions`, TLS 1.3
features) moved with it, as did `IdentityOf`, `BlockSZX` and the
`CoAPMessage`/`MessageFromCoAP` conversions that SMS, HTTP and the
Bootstrap-Server use for OSCORE. `Server.Close` no longer stops
listeners: close the transport first, then the server.

**The OSCORE layer of the Server lives in `transport/coap`**, not in
`security/oscore`: it is the CoAP binding's view of OSCORE (it wraps
go-coap connections and the core's uplink handler). `security/oscore`
stays a protocol library with no dependency on `server`. SMS uses the
layer through `coap.OSCORE.HandleCoAP` and `coap.CoAPWire`.

**The Bootstrap-Server keeps its own CoAP adapter.** It shares the
low-level parts of `transport/coap` (identity, block size, message
conversion) but not the listener: its uplink path has no observations,
no queue mode and no held sessions; it rejects short PSKs (BS-10); its
OSCORE layer answers bootstrap paths only; and it runs one deferred
session start per connection. A shared listener would need a generic
handler interface covering both cores for about 150 lines of saving.
Revisit if a third CoAP-speaking core appears.

**`regparam` became `internal/regparam`.** Only the core's Register and
Update handling and the LoRaWAN binding parse those queries; no consumer
needs the parser, and the core already exposes the parsed result on
`Registration`.

**`compat` became `leshanapi`.** The name says what it is: Leshan's
demo-server REST API over the native server.

**Tests follow the code.** CoAP, DTLS, TCP, WebSocket and OSCORE tests
moved to `transport/coap`. The core tests stay in `server/` but run as
the external package `server_test`, because they drive the core through
`transport/coap`, which imports `server`; `server/export_test.go` exposes
the few internals they check. `// Proves:` claims move with their tests.

## Moved APIs

| Before | After |
|---|---|
| `mqttbinding`, `httpbinding`, `smsbinding`, `niddbinding`, `lorawanbinding` | `transport/mqtt`, `transport/http`, `transport/sms`, `transport/nidd`, `transport/lorawan` |
| `oscore`, `est`, `cose`, `dtlssuite` | `security/oscore`, `security/est`, `security/cose`, `security/dtls` |
| `compat` | `leshanapi` |
| `regparam` | `internal/regparam` (no longer public) |
| `srv.ListenUDP`, `ListenDTLS`, `ListenTCP`, `ListenTLS`, `WebSocketHandler`, `DTLSConfig(m)`, `EnableOSCORE` | the same methods on `coap.New(srv)` |
| `server.DTLSConfig`, `CertificateModes`, `OSCORE`, `CoAPWire`, `OSCOREIdentity`, `ErrDuplicateOSCORERecipient` | `coap.` the same names |
| `server.IdentityOf`, `BlockSZX`, `CoAPMessage`, `MessageFromCoAP` | `coap.` the same names |
| `server.DTLSExtensions`, `TLS13Feature` and its constants, `SupportedTLS13Features`, `CheckTLS13Features` | `coap.` the same names |
| `srv.Close()` stopped listeners | `cb.Close()` stops them; then `srv.Close()` |
