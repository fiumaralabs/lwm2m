# nRF91 real-device test plan

**UNTESTED on hardware.** This is a proposed procedure, not an interoperability
claim. Keep this status until a run record with the results below exists. A
simulated-client run only tests the checker, not the modem, board, firmware,
DTLS, carrier, power behavior, or FOTA.

## Test setup

- Test nRF9160 DK and nRF9151 DK separately. Record board revision, modem
  firmware version, SIM/carrier, LTE mode, country, and signal conditions.
- Use a pinned Nordic nRF Connect SDK (NCS) release and its
  [LwM2M client sample](https://github.com/nrfconnect/sdk-nrf/tree/main/samples/cellular/lwm2m_client).
  Record the SDK commit, Zephyr commit, sample configuration, toolchain and
  build target. Check the sample's documentation for the pinned release, since
  supported boards, build targets and options may change.
- Use an isolated server and a test device. Ensure the device can reach the
  server's CoAP/DTLS address and the test operator can reach its HTTP API.
  Do not use production devices, credentials, or firmware for this plan.
- Pre-provision the same endpoint in `/v1/devices`. Provision a unique random
  DTLS PSK and identity on both the test server and device through their
  supported secure provisioning methods. The credentials endpoint is described
  in [API.md](API.md). Do not commit, print, or include PSKs, API tokens or SIM
  identifiers in the run record. Configure the client for the matching CoAPS
  server URI and identity. Confirm the selected sample actually exposes the
  resources used below before running the checker.
- Use an API key with `devices:read` and `operations:write` scopes and access
  to the test device's tenant. Provide it through `LWM2M_API_TOKEN` in the
  environment without placing it in shell history. Use a protected connection
  for remote API access. The checker requires Python 3.

## Scripted checks

Set `LWM2M_REAL_DEVICE_EP`, `LWM2M_API_URL` (default
`http://127.0.0.1:8080`) and `LWM2M_API_TOKEN`, then run
`./scripts/real-device-check.sh`. With no endpoint it exits 0 and prints
`SKIP: no device`. With an endpoint it exits nonzero if a check fails. A
registration is required before the script starts. It waits up to 30 seconds
for each operation and observation; a sleeping device may need to wake first.

| Check | Expected result |
| --- | --- |
| Register | `/v1/devices/{endpoint}/registration` returns the current registration. |
| Read | Read `/3/0/15` returns a typed value. |
| Write | Write the value just read back to `/3/0/15`, then read it again and compare. This is still a device write. |
| Execute | Execute `/3303/0/5605` (reset measured min/max). |
| Observe | Observe `/3303/0/5700` becomes active; the script cancels it. This does not prove notifications arrived. |
| FOTA | Explicitly skipped by the script. Use the manual procedure below. |

The sample may not expose those resources. Set `LWM2M_WRITE_PATH`,
`LWM2M_EXECUTE_PATH`, and `LWM2M_OBSERVE_PATH` to suitable resources in the
actual sample. Select a writable resource for which writing the same value is
safe, a harmless execute resource, and an observable resource. Executing can
change device state; never point it at reboot or factory reset without an
explicit test plan. Check the configured paths and consequences before running.
Do not interpret a skipped or unsupported check as a pass.

## Manual observation and firmware checks

1. Create an observation through `/v1/devices/{endpoint}/observations`, wait
   for active, change the measured value or wait for a configured reporting
   interval, and verify a new notification event and its value. Cancel the
   observation and confirm it ends. Record timestamps and redacted event IDs.
2. Build a compatible, signed test image using the sample's documented update
   mechanism. Confirm recovery access and power stability. Record image version,
   hash, signing procedure (not keys), and the supported transport. The
   simulator does not implement the board's firmware update path.
3. Use the firmware API described in [API.md](API.md) and the sample's FOTA
   instructions to deliver that image to the isolated device. Check the firmware
   object state and update result, then verify the device reconnects and reports
   the intended firmware version after any reboot. Record actual transitions,
   errors and timeouts. A successful upload or accepted request alone is not a
   successful update. Do not deploy an image to an unattended device.

## Run record

For each board record date, operator, board/revision, pinned NCS/Zephyr and
modem versions, sample configuration, network conditions, server commit and
command configuration (without credentials). Record PASS/FAIL/SKIP separately
for registration, read, write and readback, execute, observation establishment,
notification, cancellation and each FOTA stage. Include redacted logs or event
IDs, firmware hash and observed version. Explain unsupported checks and attach
failure details. Do not change the UNTESTED status based on a simulated run.
