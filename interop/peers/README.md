# Interop with real LwM2M clients

These tests run our LwM2M Server and Bootstrap-Server in-process (packages `server`, `bootstrap`, `fota`) against open-source LwM2M clients built from pinned upstream sources:

| Peer | Upstream | Pin | Binary in the image | Driven by |
|---|---|---|---|---|
| Eclipse Wakaama `lwm2mclient` (NoSec, client-initiated bootstrap) | eclipse-wakaama/wakaama | `94ff56f77a2d24a5890e0e703809a47633aa7d4b` | `wakaama-client` | CLI flags + stdin |
| Eclipse Wakaama `lwm2mclient_tinydtls` (PSK) | same | same | `wakaama-client-dtls` | CLI flags + stdin |
| AVSystem Anjay demo, LwM2M 1.2, mbedTLS 3.6.7 (PSK, X.509, CID), CoAP/TCP, Bootstrap-Pack, FOTA | AVSystem/Anjay | tag `3.15.0` = `fdd70854c46f676acda179ad3a1b760eceada4db` | `anjay-demo` | CLI flags + stdin |
| AVSystem Anjay Lite integration `test_app`, LwM2M 1.2, mbedTLS 3.6.7 (PSK) | AVSystem/Anjay-lite | `b33821b042637315c83c548e0bc8286d6dbf7316` (3.0.2) | `anjay-lite-app` | its JSON-RPC socket |

mbedTLS is built from `v3.6.7` because the distro's 2.28 has no RFC 9146 Connection ID.

## Licensing

Anjay and Anjay Lite are under the **AVSystem non-commercial licences** (since Anjay 3.10). **Nothing from either is vendored in this repository:** no source, no test data, no vectors. The Dockerfile clones and builds them from upstream at image build time, and the image is a local or CI artifact that is not published. The tests only talk to the binaries over the network or their documented CLI/RPC. The Anjay firmware package header and the Anjay Lite FOTA-mock package (Adler-32 prefix) are built by our Go code from the documented formats. Wakaama is EPL-2.0 / BSD-3-Clause.

## Running

Inside the image (the test files carry `//go:build interop`, so the regular suite never sees them):

```sh
# once, or whenever a pin in the Dockerfile changes (~10 min; later builds are cached)
docker build -f interop/peers/Dockerfile --target peers -t lwm2m-peers .

# run against the working tree
docker run --rm -v "$PWD:/src" -w /src \
    -v lwm2m-gomod:/root/go/pkg/mod -v lwm2m-gocache:/root/.cache/go-build \
    lwm2m-peers go test -tags interop -count=1 -v ./interop/peers/

# one client or test; PEERS_LOG=1 prints every peer's full output
docker run --rm -e PEERS_LOG=1 -v "$PWD:/src" -w /src lwm2m-peers \
    go test -tags interop -count=1 -v -run 'TestAnjay(Queue|Bootstrap)' ./interop/peers/
```

CI (`.github/workflows/peers-interop.yml`, on push to main, nightly and on demand) builds the `test` target. That target copies the module in and builds `lwm2md` and a `-race` test binary, then runs `peers.test` inside the container. Outside the image, each test skips: it needs `WAKAAMA_CLIENT`, `WAKAAMA_CLIENT_DTLS`, `ANJAY_DEMO` and `ANJAY_LITE_APP`, which point at the binaries. On a Mac, Docker (colima) runs an arm64 Linux VM. CI runs on amd64 with the same Dockerfile.

Each test starts its own server with UDP, DTLS (CID length 6) and TCP listeners on `127.0.0.1:0`, starts one client process, and asserts on behaviour. That covers decoded values, cross-format agreement, read-back after writes, notifications carrying the new value, no notification after Cancel, queued requests held until wake-up, bootstrapped values used by the DM registration, and client-side image validation after FOTA. A failing test logs the peer's output.

## Results

Last run: all 29 tests and 18 subtests pass at the pins above, with the `-race` test binary and no data races.

`pass` = exercised and asserted. `unsupported` = the client (or its example build) lacks it, and the cell says how we know. `n/r` = not run here.

| Feature | Wakaama | Anjay 3.15 | Anjay Lite 3.0.2 |
|---|---|---|---|
| Register / Update / De-register (NoSec) | pass (1.1) | pass (1.0, 1.1, 1.2) | pass (1.2; De-register via Disable) |
| DTLS PSK | pass; a wrong key does not register | pass; also CCM_8-only (T68) | pass (CCM_8 only) |
| DTLS RPK | unsupported (tinydtls example) | unsupported (OSS rejects mode 1) | unsupported |
| DTLS X.509 | unsupported | pass (client cert CN = ep, server cert pinned, DANE usage 3) | n/r |
| DTLS Connection ID (NAT rebinding, no re-handshake) | unsupported | pass | n/r |
| New handshake from the same port (T74) | n/r | pass (after bootstrap) | n/r |
| CoAP over TCP | unsupported | pass (Register, Read, Observe, Update, De-register) | unsupported (removed in 2.0) |
| Read: text / opaque | pass / pass (via /5/0/0) | pass / pass | pass / n/r |
| Read: TLV | pass | pass | unsupported (decode only) |
| Read: SenML JSON | pass | pass | unsupported (4.15, asserted) |
| Read: SenML CBOR | unsupported (off in example) | pass | pass |
| Read: LwM2M CBOR | unsupported | pass | pass (also the no-Accept default) |
| Read: OMA JSON / CBOR 60 | pass / unsupported (1.0 only) | pass (output only) / pass | unsupported / n/r |
| Write (replace) in each format the client decodes | pass (text, TLV, SenML JSON, OMA JSON) | pass (text, TLV, CBOR, SenML JSON/CBOR, LwM2M CBOR, opaque) | pass (text, CBOR, SenML CBOR, LwM2M CBOR, TLV) |
| Partial Update / multi-instance resource | pass / n/r | pass / n/r | pass / pass |
| Execute | pass | pass (counter increments; 4.05 on non-executable) | n/r (no executable resource) |
| Create / Delete | pass (Update follows, T38) | pass (TLV, SenML CBOR) | pass |
| Discover / depth (1.2) | pass / unsupported | pass / pass (0, 1, 2) | pass / pass (1) |
| Block1 / Block2 | pass up to 2048 B (4.13 above, client limit) | pass (5000 B both ways) | pass (5 KiB FOTA push) |
| Write-Attributes + Observe / Notify / Cancel | pass (pmax) | pass (pmax) | pass |
| Attributes in the Observe request (1.2) | unsupported | pass (gt) | pass (gt) |
| Confirmable notifications (con=1) | unsupported (NON only) | pass | n/r |
| Read-Composite | unsupported (T32: fails within the caller's bound, client still usable) | pass (bodies SenML CBOR/JSON, ETCH CBOR/JSON) | pass (SenML CBOR, ETCH CBOR) |
| Write-Composite | unsupported | pass (SenML CBOR/JSON, LwM2M CBOR; atomic on error) | pass |
| Observe-Composite | unsupported | pass | unsupported (off by default) |
| Send | pass (SenML JSON) | pass (timestamps present) | pass (SenML CBOR, LwM2M CBOR) |
| Client-initiated bootstrap | pass (with Discover) | pass (NoSec and PSK over DTLS) | pass (pct 112 honoured) |
| Bootstrap-Pack | unsupported | pass; refused-Pack fallback pass (T73) | unsupported |
| Server-initiated bootstrap (/1/x/9) | n/r | pass | n/r |
| Registration Update Trigger / Disable (/1/x/8, /1/x/4) | n/r | pass | n/r |
| Queue mode | unsupported ("Q" sent, no behaviour) | pass (wake by re-Register, T24) | pass |
| FOTA push (/5/0/0 Block1, Execute) | pass (stub object) | pass (image CRC-checked by the client) | pass |
| FOTA pull (/5/0/1 CoAP, our `fota.FileServer`) | unsupported (/5/0/9 = push only) | pass | pass |

## Server bugs found and fixed

Each fix has a regression test in the regular suite.

1. **Queued requests were dropped when a queue-mode client woke up with a re-Register.** Anjay re-registers whenever its NoSec socket reopens, and so do Anjay Lite and modems after PSM (T24). The replaced registration's queue failed every waiter with `ErrQueueDropped`, so a queued request never reached such a client. Now queued requests follow the replacing registration (`server/queue.go` `handover`, `server/rd.go`). Test: `server.TestQueuedRequestFollowsReRegister`.
2. **A DTLS handshake from a port that still had a session hung.** pion's UDP demux sent the new epoch-0 ClientHello to the stale association. Anjay reuses its last local port after bootstrap, a re-Register or a restart. Fixed in our pion/dtls fork (patch 5 in [LWM2M.md](https://github.com/fiumaralabs/dtls/blob/lwm2m-v3/LWM2M.md)), RFC 6347 §4.2.8. Test: `server.TestDTLSNewHandshakeFromSamePort`, with and without CID.
3. **The Bootstrap-Server's `AutoIDForSecurityObject` did not protect the BS account in a Bootstrap-Pack.** A Pack has no Discover, and Anjay 3.15 sends no `acc`, so a Pack /0/1 replaced Anjay's BS account and later Bootstrap-Request Triggers failed. Now that combination gets 4.05 and the client falls back to Bootstrap-Request (`bootstrap/server.go` `packRequest`, T73). Test: `bootstrap.TestPackAutoIDNeedsAcc`.

## Client behaviour worth knowing (documented, not bugs of ours)

- **Anjay demo** de-registers only on stdin EOF. SIGINT kills it without a De-register.
- **Anjay demo** does not notify a server about changes **that server** wrote, unless `--enable-self-notify` is set. The Observe tests set it.
- **Anjay demo** keeps its BS account at `/0/1`. Bootstrap configs must not reuse that IID: use `AutoIDForSecurityObject` (classic flow) or another IID (Pack).
- **Anjay OSS** has no RPK: security mode 1 is rejected locally ("unsupported security mode").
- **Wakaama** answers 4.13 to Block1 bodies over 2048 bytes (`WAKAAMA_COAP_MAX_MESSAGE_SIZE`). Its example client waits 10 s before bootstrapping (Client Hold Off Time /0/x/11). Its /5 is a push-only stub (State 1 → 2 on Update). At `94ff56f7` the LwM2M 1.0 example does not compile (`object_server.c` uses 1.1-only fields), so 1.0 is covered by Anjay `-V 1.0`.
- **Anjay Lite `test_app`** shuts down without a De-register. `disable_server` de-registers.

## Remaining gaps

X.509, CID and Execute on Anjay Lite are not run: `test_app` supports X.509, but the test is not written yet, and its test object has no executable resource. OSCORE, EST, DTLS 1.3, SMS, NIDD, MQTT, HTTP and the Gateway object have no open-source client peer (spec/client-ecosystem.md §4.3).
