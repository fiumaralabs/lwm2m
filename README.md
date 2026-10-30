# lwm2m

A Go implementation of an OMA LwM2M 1.2.2 Server and Bootstrap-Server that also serves 1.0 and 1.1 clients.
The goal is the most spec-compliant and interoperable LwM2M server there is.

Module: `github.com/fiumaralabs/lwm2m`

## How compliance is tracked

[`spec/`](spec/README.md) holds the specification this code implements: every normative requirement of LwM2M 1.0.2, 1.1.1 and 1.2.2 (Core and Transport) as numbered rows, the OMA ETS test cases, client-ecosystem quirks, and golden codec vectors.

`TestSpecCoverage` (`go test -run TestSpecCoverage .`) ties the two together:
- every requirement ID must be claimed by a test comment `// Proves: <ID>`,
- or be listed, with the reason, in `spec/coverage-pending.txt` (not done yet) or `spec/coverage-informative.txt` (no implementable behaviour).

The spec is fully implemented when `coverage-pending.txt` is empty. After adding tests, run `./scripts/prune-coverage.py`.

What is left to make it feature-complete and production-ready is in [ROADMAP.md](ROADMAP.md).

## Packages

The layout and the reasons behind it are in [doc/ARCHITECTURE.md](doc/ARCHITECTURE.md).

| Package | What it is |
|---|---|
| `lwm2m` (root) | Paths, typed values, nodes, content formats |
| `codec/...` | Every data format: text, opaque, TLV, CBOR, SenML JSON/CBOR, SenML-ETCH, OMA JSON, LwM2M CBOR (`codec/all` registers them) |
| `link`, `attr` | CoRE link-format, notification attributes |
| `model` | OMA object registry (embedded objects 0–28 with version history), version resolution, validation |
| `acl` | Access Control (/2) model |
| `server` | LwM2M Server core, binding-neutral: Registration, Device Management, Information Reporting, queue mode, Send |
| `transport/coap` | CoAP bindings for the Server: UDP, DTLS 1.2/1.3 (PSK, X.509, CID), TCP/TLS, WebSockets; OSCORE |
| `transport/mqtt`, `transport/http` | LwM2M over MQTT (M) and HTTP (H) |
| `transport/sms`, `transport/nidd`, `transport/lorawan` | LwM2M over SMS (S), Non-IP/NIDD (N) and LoRaWAN |
| `security/oscore`, `security/est`, `security/cose`, `security/dtls` | OSCORE (RFC 8613), EST over CoAPs (RFC 9148), COSE_Encrypt0, DTLS suites and checks pion lacks |
| `bootstrap` | Bootstrap-Server: Bootstrap-Request, Pack-Request, server-initiated bootstrap |
| `gateway` | LwM2M Gateway (/25, /26): end devices behind a gateway |
| `fota` | Firmware update orchestration (push and pull) and a block-wise file server |
| `leshanapi` | Leshan-compatible REST API, so the Zephyr interop suite runs unchanged |
| `testclient` | Scriptable reference LwM2M client used to prove server behaviour |
| `cmd/lwm2md` | Server binary |
| `cmd/lwm2m-client` | Demo LwM2M client: a simulated sensor (see Try it) |

## Try it

A public demo server runs at [lwm2m.fiumaralabs.com](https://lwm2m.fiumaralabs.com). `cmd/lwm2m-client` is a simulated battery-powered sensor (Device, Connectivity Monitoring, Location, Temperature, Humidity, Voltage) built on `testclient`; it registers, answers Read/Observe/Discover/Write-Attributes, sends Updates and deregisters on Ctrl-C. Needs Go 1.21 or newer.

```sh
# NoSec
go run github.com/fiumaralabs/lwm2m/cmd/lwm2m-client@deploy-demo -server coap://lwm2m.fiumaralabs.com:5683
# DTLS-PSK (public demo key; the PSK identity is the endpoint name)
go run github.com/fiumaralabs/lwm2m/cmd/lwm2m-client@deploy-demo -server coaps://lwm2m.fiumaralabs.com:5684 -psk 000102030405060708090a0b0c0d0e0f
```

The endpoint name is random (`demo-xxxxxx`) unless you pass `-ep`; it is listed publicly on the page. Other flags: `-lwm2m 1.0|1.1|1.2`, `-lifetime` (default 300 s, at most 3600), `-binding UQ` (queue mode), `-send 30s` (LwM2M 1.1+ Send). `-h` lists them.

## Quick start

For `lwm2md` JSON configuration and flag precedence, see [doc/config.md](doc/config.md).

```go
models := server.NewModels(model.Default())
srv := server.New(server.Config{Schema: models.Schema, Validator: models})
cb := coap.New(srv) // transport/coap
cb.ListenUDP(":5683")
cb.ListenDTLS(":5684", coap.DTLSConfig{})
resp, err := srv.Read(ctx, "urn:imei:123", lwm2m.MustParsePath("/3/0"), server.ReadOptions{})
```

## License

Apache License 2.0, see [LICENSE](LICENSE). Third-party test data and object definitions are attributed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
