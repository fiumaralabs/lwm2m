# Zephyr LwM2M interop suite against lwm2md

Runs Zephyr's own interop suite, `tests/net/lib/lwm2m/interop` (66 pytest
tests, the OMA ETS 1.1 subset Zephyr checks against Leshan), unchanged,
with `lwm2md` in place of the Leshan demo server and bootstrap server.
The suite inventory and the Leshan REST contract it depends on are in
`spec/zephyr-interop.md`.

## Run

```sh
interop/zephyr/run.sh                                  # whole suite, about 30 min
interop/zephyr/run.sh --pytest-args='-k int_102'       # one test (args go to twister)
interop/zephyr/run.sh --pytest-args='-m "not slow"'    # skip the @slow tests (about 5 min)
```

Requirements: Docker. On macOS, colima or Docker Desktop work; the
default `out/` directory is under the repo, because colima only shares
`$HOME` with its VM. Results go to `interop/zephyr/out/`:

- `twister-out/twister_report.xml`: one testcase per pytest test.
- `twister-out/native_sim*/.../net.lwm2m.interop/handler.log`: DUT console.
- `twister-out/.../twister_harness.log` and `report.xml`: pytest's view.
- `lwm2md.log`: the server (`-v`: registrations, updates, notifications,
  bootstrap steps).

Environment: `PLATFORM` (the twister platform, see below), `LWM2MD_ARGS`
(extra lwm2md flags), `OUT`, `IMAGE`, `DOCKER_BUILD_ARGS`
(e.g. `--build-arg ZEPHYR_VERSION=v4.5.0`).

## What the container does

`Dockerfile` (built from the repo root):

1. `golang` stage: `go build ./cmd/lwm2md`.
2. `ubuntu:24.04` stage: host gcc, cmake, ninja, dtc, Python venv with west
   and Zephyr's build and run test requirements. A shallow Zephyr at
   `ZEPHYR_VERSION` (default `v4.4.2`), with the west project filter cut
   down to the modules the sample needs: `mbedtls`, `tf-psa-crypto`,
   `zcbor`, `net-tools`. The DUT is pre-built once to warm ccache, and that
   build fails if any `prj.conf` option was silently dropped. A missing
   `zcbor` once turned SenML-CBOR off, and every write then failed with
   `Unknown content type 112`.
3. The final image adds the `lwm2md` binary. Editing Go code only rebuilds
   steps 1 and 3.

`entrypoint.sh`, at run time:

1. `tools/net-tools/net-setup.sh start` creates the `zeth` TAP with
   192.0.2.2/24, the address the suite expects for Leshan. This needs
   `--cap-add NET_ADMIN --device /dev/net/tun`.
2. `lwm2md -v` listens on all interfaces with Leshan's ports: CoAP
   5683/udp, CoAPs 5684/udp, bootstrap 5783/udp and 5784/udp, REST
   8080/tcp (server) and 8081/tcp (bootstrap). The harness defaults
   (`--leshan_rest_api=http://localhost:8080/api`,
   `--leshan_bootstrap_rest_api=http://localhost:8081/api`) already point
   at it, so nothing in the suite is changed or overridden.
3. `twister -p $PLATFORM --force-platform -T zephyr/tests/net/lib/lwm2m/interop --enable-slow -vv`.

Platform: on x86_64 (CI) the DUT is `native_sim`, as upstream. Elsewhere
(arm64 hosts, Apple silicon) it is `native_sim/native/64`, which builds
and runs natively on aarch64 Linux. `--force-platform` is needed because
`tests.yaml` only allows `native_sim` and `qemu_x86`. The
`boards/native_sim.conf` overlay (PTY on stdio, real-time slowdown, ASAN)
is passed as `EXTRA_CONF_FILE` because the qualified target would
otherwise not pick it up. Nothing in the suite's sources changes.

## CI

`.github/workflows/zephyr-interop.yml` builds the same image on
`ubuntu-latest` and runs it nightly (03:47 UTC) and on `workflow_dispatch`,
with an optional `twister_args` input. It writes a per-test summary table
to the job summary and uploads `out/` as the `zephyr-interop` artifact.

## Results

Zephyr v4.4.2, `native_sim/native/64` on aarch64 (colima), 2026-10-06:
**66/66 pass**, pytest session 424 s. Every test module bootstraps over
DTLS-PSK (5784), then registers over DTLS-PSK with CID (5684), except
`test_nosec.py` (NoSec, 5683).

Server bugs the suite found, all fixed with Go regression tests:

| Symptom in the suite | Root cause | Fix |
|---|---|---|
| int-109 never sees `Queue mode RX window closed`; every request after an idle period fails with `cannot write to connection: EOF` | go-coap's default inactivity monitor closes a UDP/DTLS conn after 16 s of silence, which drops the DTLS session and CID of a registered client | `transport/coap/keepconn.go`: conns that carry a live registration are never closed for inactivity |
| `test_blockwise_1..3`: write to `/5/0/0` and `/19/0/0/0` times out at 93 s | downlink Block1 used 1024-byte blocks (a 1087-byte DTLS+CID datagram). The DUT has 8×128 B of RX net buffers and drops it silently | 512-byte blocks (SZX 5, Californium's default) on every listener, `coap.BlockSZX` |
| `test_blockwise_4`: 4 KiB Send gets 4.00 | go-coap reassembles Block1 by token; Zephyr uses a new token per block (allowed by RFC 7959), so only the last block reached the Send handler | `transport/coap/block1.go`: Block1 reassembly keyed by conn, method, Uri-Path and Request-Tag |

Harness issues, not server issues:

- `tests.yaml` `timeout: 600` covers the whole pytest session, not one
  test. A failing run (each blockwise failure waits out the request
  timeout) overruns it, and twister then reports only "Pytest timeout"
  with no per-test results. `entrypoint.sh` passes `--timeout-multiplier 5`.
- Tests change the session-scoped `leshan.format` and timeouts and only
  restore them on success. One failure (e.g. `test_blockwise_1` leaving
  `format=OPAQUE`, or int-220 leaving `JSON`) cascades into unrelated
  failures (`opaque: needs exactly one opaque value at /1/0/1`, composite
  reads with `JSON`). The same applies to device state: int-304 leaves
  `/1/0/2,3 = 0`, which fails int-306, and int-1630 leaves `/16/1`, which
  fails int-1635. When triaging, fix the first failure first.
- `spec/zephyr-interop.md` §5 quirks: `test_attribute_less_than` sets `gt`,
  not `lt`; TIME values are sent in seconds while Leshan reads ms;
  `test_blockwise_3` restores `format` from the loop variable.
- README typo upstream: the DTLS bootstrap port is 5784, not 5684.

After the move to upstream pion/dtls v4.0.0-rc.3 (2026-10-07, same
platform): two full runs had 63/66 and 62/66 passing, with different
tests failing each time (int_309, int_310, attribute_less_than; then
int_301, int_308, int_309, int_105), all waiting on a notification or a
DUT log line. In both runs lwm2md received nothing from the DUT for
minutes at the time (the DUT stalled; its uptime clock fell behind), and
every one of those tests passes when rerun on its own. A run of the
pre-move commit on the same machine that day failed int_301 the same way.

The same commit on CI (`ubuntu-latest`, amd64, run 37613335518) passed
66/66 in 428 s, so the local failures above come from the DUT stalling
under load on the colima VM, not from the server. If a local run fails
only on notification or log waits, rerun those tests on their own or use CI.
