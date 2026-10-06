# Real-device runbook: Zephyr LwM2M clients over cellular against lwm2md

This runbook covers two client stacks:

- **A. nRF9160 / nRF9151 (nRF Connect SDK).** The modem terminates DTLS
  (socket offload), so CID and session caching are modem features.
- **B. Zephyr + mbedTLS.** Any board where Zephyr's own TLS sockets run
  DTLS, e.g. an nRF52840/STM32 board with a PPP cellular modem
  (`modem_cellular`), or an HL78xx with the Zephyr IP stack.

It checks five things over a real carrier NAT. Each section ends with
exact pass/fail criteria.

1. Bootstrap, then registration with DTLS-PSK.
2. DTLS Connection ID (RFC 9146) is negotiated.
3. A NAT rebind is survived: the Update arrives from a new address and
   port, with no new handshake.
4. Queue mode: a request made while the device sleeps is delivered when it
   wakes.
5. FOTA pull: the device downloads the image from lwm2md with CoAP Block2.

Kconfig names come from Zephyr v4.4.2 (`subsys/net/lib/lwm2m/Kconfig`,
`samples/net/lwm2m_client`). NCS sample option names (`CONFIG_APP_*`,
`CONFIG_LWM2M_CLIENT_UTILS_*`) change between NCS releases, so check them
against your NCS version's `samples/cellular/lwm2m_client/Kconfig` before
you build.

---

## 0. Server host

You need a host with a public IPv4 address (a VM is fine). The device
reaches it over the carrier NAT.

```sh
go build -o lwm2md ./cmd/lwm2md
mkdir -p fw && cp <signed-image>.bin fw/app.bin     # for step 5
./lwm2md -v -cid 6 -fw-dir fw 2>&1 | tee lwm2md.log
```

| Port | Proto | Purpose | Expose |
|---|---|---|---|
| 5683 | UDP | CoAP NoSec (unused here) | optional |
| 5684 | UDP | CoAPs LwM2M server (PSK, CID, resumption) | **public** |
| 5783 / 5784 | UDP | Bootstrap-Server NoSec / DTLS | 5784 **public** |
| 5693 / 5694 | UDP | firmware CoAP / CoAPs (`-fw-dir`) | **public** |
| 8080 / 8081 | TCP | Leshan-compatible REST (server / bootstrap) | **localhost or VPN only**: no auth |

Expected startup lines in `lwm2md.log`: `coaps listening on [::]:5684`,
`bs coaps listening on [::]:5784`, `firmware /app.bin (N bytes)`,
`fw coaps listening on [::]:5694`.

Use one capture for the whole session. It is the ground truth for CID,
handshakes and rebinds:

```sh
sudo tshark -i any -f 'udp port 5684 or udp port 5784 or udp port 5694' -w dev.pcapng
# later, summarise:
tshark -r dev.pcapng -Y dtls -T fields -e frame.time_relative -e ip.src -e udp.srcport \
  -e dtls.record.content_type -e dtls.handshake.type
```

The capture records `content_type` 22 for handshake, 23 for application
data and **25 for `tls12_cid`** (records that carry a CID). In
`handshake.type`, 1 is ClientHello, 2 is ServerHello and 16 is
ClientKeyExchange. A full PSK handshake has a 16; a resumed one does not.

### Provision the device (REST, from the server host)

`EP` is the endpoint name and the PSK identity, for example `urn:imei:<IMEI>`.
Keys are at least 16 bytes. The Bootstrap-Server refuses shorter PSKs
(BS-10).

```sh
EP='urn:imei:350457790000000'; HOST=<public-ip>
BSKEY=$(openssl rand -hex 16); KEY=$(openssl rand -hex 16)
# bootstrap credential
curl -sf -X PUT localhost:8081/api/security/clients -H 'content-type: application/json' \
  -d "{\"endpoint\":\"$EP\",\"tls\":{\"mode\":\"psk\",\"details\":{\"identity\":\"$EP\",\"key\":\"$BSKEY\"}}}"
# LwM2M server credential
curl -sf -X PUT localhost:8080/api/security/clients -H 'content-type: application/json' \
  -d "{\"endpoint\":\"$EP\",\"tls\":{\"mode\":\"psk\",\"details\":{\"identity\":\"$EP\",\"key\":\"$KEY\"}}}"
# bootstrap config (Leshan BootstrapConfig JSON; byte arrays are ints)
ids=$(python3 -c "import sys;print(list(sys.argv[1].encode()))" "$EP")
key=$(python3 -c "import sys;print(list(bytes.fromhex(sys.argv[1])))" "$KEY")
curl -sf -X POST localhost:8081/api/bootstrap/$EP -H 'content-type: application/json' -d '{
 "toDelete":["/0","/1"],
 "servers":{"0":{"shortId":1,"lifetime":3600,"defaultMinPeriod":1,"notifIfDisabled":false,"binding":"U"}},
 "security":{"1":{"uri":"coaps://'$HOST':5684","bootstrapServer":false,"securityMode":"PSK",
   "publicKeyOrId":'"$ids"',"secretKey":'"$key"',"serverId":1,"clientOldOffTime":1}}}'
```

Leave binding at `U`. A LwM2M 1.1 client signals queue mode with the `Q`
registration parameter (`queue=true` in the log). `UQ` is the 1.0 form
and is deprecated in 1.1.

To skip bootstrap, put only the server credential and point the device
straight at `coaps://$HOST:5684`.

---

## 1A. Build: nRF9160 / nRF9151 (NCS `samples/cellular/lwm2m_client`)

Modem firmware: nRF9160 needs **mfw 1.3.5 or later** for DTLS CID. nRF9151
needs **mfw 2.0.x** (CID is supported). Check with `AT+CGMR`.

`overlay-lwm2md.conf`:

```ini
# LwM2M 1.1, SenML-CBOR
CONFIG_LWM2M_VERSION_1_1=y
CONFIG_LWM2M_RW_SENML_CBOR_SUPPORT=y
CONFIG_ZCBOR=y
CONFIG_ZCBOR_CANONICAL=y
# DTLS: modem-offloaded; CID and session cache are passed to the modem
CONFIG_LWM2M_DTLS_SUPPORT=y
CONFIG_LWM2M_DTLS_CID=y
CONFIG_LWM2M_TLS_SESSION_CACHING=y
# Bootstrap (omit both to go straight to 5684)
CONFIG_LWM2M_RD_CLIENT_SUPPORT_BOOTSTRAP=y
CONFIG_LWM2M_CLIENT_UTILS_SERVER="coaps://<public-ip>:5784"
# PSK of the first server contacted: BSKEY with bootstrap, else KEY (hex)
CONFIG_APP_LWM2M_PSK="<BSKEY hex>"
# Queue mode: with CID the socket stays open and only polling stops
CONFIG_LWM2M_QUEUE_MODE_ENABLED=y
CONFIG_LWM2M_QUEUE_MODE_UPTIME=30
CONFIG_LWM2M_RD_CLIENT_STOP_POLLING_AT_IDLE=y
# Long enough idle that the carrier NAT binding expires (most are 30-120 s UDP)
CONFIG_LWM2M_ENGINE_DEFAULT_LIFETIME=3600
CONFIG_LWM2M_UPDATE_PERIOD=300
# FOTA pull (NCS uses its own downloader for the image)
CONFIG_LWM2M_CLIENT_UTILS_FIRMWARE_UPDATE_OBJ_SUPPORT=y
CONFIG_LWM2M_FIRMWARE_UPDATE_PULL_SUPPORT=y
CONFIG_LOG=y
CONFIG_LWM2M_LOG_LEVEL_INF=y
```

```sh
west build -p -b nrf9151dk/nrf9151/ns samples/cellular/lwm2m_client -- \
  -DEXTRA_CONF_FILE=overlay-lwm2md.conf
west flash
```

The sample writes the PSK to the modem's security tag at boot. The
endpoint name defaults to `urn:imei:<IMEI>`, so use that as `EP`.

## 1B. Build: Zephyr + mbedTLS (`samples/net/lwm2m_client`)

`overlay-lwm2md.conf` (on top of the sample's `overlay-dtls.conf`,
`overlay-lwm2m-1.1.conf` and `overlay-queue.conf`, plus your modem's
overlay, e.g. `overlay-hl7800.conf`):

```ini
CONFIG_LWM2M_VERSION_1_1=y
CONFIG_LWM2M_DTLS_SUPPORT=y
# CID: needs mbedTLS CID; LWM2M_DTLS_CID defaults to y when it is on
CONFIG_MBEDTLS_SSL_DTLS_CONNECTION_ID=y
CONFIG_LWM2M_DTLS_CID=y
# Session resumption (Zephyr TLS socket session cache)
CONFIG_LWM2M_TLS_SESSION_CACHING=y
CONFIG_NET_SOCKETS_TLS_MAX_CLIENT_SESSION_COUNT=2
# PSK suite the server prefers (SEC-04): CCM_8 via PSA
CONFIG_MBEDTLS_KEY_EXCHANGE_PSK_ENABLED=y
CONFIG_PSA_WANT_ALG_CCM=y
# Endpoint = PSK identity
CONFIG_NET_SAMPLE_LWM2M_ID="<EP>"
CONFIG_NET_SAMPLE_LWM2M_PSK="<BSKEY hex, or KEY hex without bootstrap>"
CONFIG_LWM2M_RD_CLIENT_SUPPORT_BOOTSTRAP=y
CONFIG_NET_SAMPLE_LWM2M_SERVER="coaps://<public-ip>:5784"
# Queue mode
CONFIG_LWM2M_QUEUE_MODE_ENABLED=y
CONFIG_LWM2M_QUEUE_MODE_UPTIME=30
CONFIG_LWM2M_RD_CLIENT_STOP_POLLING_AT_IDLE=y
CONFIG_LWM2M_ENGINE_DEFAULT_LIFETIME=3600
CONFIG_LWM2M_UPDATE_PERIOD=300
# FOTA pull via Block2
CONFIG_LWM2M_FIRMWARE_UPDATE_OBJ_SUPPORT=y
CONFIG_LWM2M_FIRMWARE_UPDATE_PULL_SUPPORT=y
CONFIG_LWM2M_COAP_BLOCK_SIZE=512
CONFIG_LWM2M_SHELL=y
```

```sh
west build -p -b <board> samples/net/lwm2m_client -- \
  -DEXTRA_CONF_FILE="overlay-dtls.conf;overlay-lwm2m-1.1.conf;overlay-queue.conf;overlay-lwm2md.conf"
```

Without `CONFIG_MBEDTLS_SSL_DTLS_CONNECTION_ID` the build warns
`LWM2M_DTLS_CID ... got the value 'n'`. Treat any such Kconfig warning as
a failed build.

---

## 2. Checks

Watch three things together: the device console, `lwm2md.log`, and the
event stream (`curl -N localhost:8080/api/event`).

### 2.1 Bootstrap and registration

Device:
```
<inf> net_lwm2m_rd_client: Bootstrap registration done!
<inf> net_lwm2m_rd_client: Bootstrap data transfer done!
<inf> net_lwm2m_rd_client: Registration Done (EP='<registration id>')
```
lwm2md:
```
bootstrap <EP>: steps=[{DELETE /0 ... Deleted} {DELETE /1 ... Deleted} {PUT /0/1 ... Changed} {PUT /1/0 ... Changed} {POST / <nil> Changed}] err=<nil>
registered <EP> id=<registration id> addr=<carrier-ip>:<port> lifetime=1h0m0s binding=U queue=true
```
- **Pass:** both log sets appear. `curl localhost:8080/api/clients/$EP`
  shows `"secure":true,"queuemode":true`. The ids in both logs are the
  same.
- **Fail:** `bootstrap ...: err=...` (the error names the step), or the
  device logs `Failed with code 4.0` / `4.3`. If no `registered` line
  appears and pcap shows only content type 21 (alert) on 5684, the PSK or
  identity is wrong.

### 2.2 DTLS Connection ID

In pcap, after the first handshake on 5684:
- The ClientHello has extension `connection_id` (type 54), and so does the
  ServerHello (6 bytes from lwm2md, `-cid 6`).
- Every later client→server record has **content_type 25**.
- **Pass:** all client records after the handshake are type 25.
- **Fail:** type 23 records from the device mean CID was not negotiated.
  Check modem firmware (A) or `CONFIG_MBEDTLS_SSL_DTLS_CONNECTION_ID` (B).
  lwm2md always offers CID unless `-cid 0` is set.

### 2.3 NAT rebind survives (Update without a new handshake)

Leave the device idle longer than the carrier NAT timeout. With
`UPDATE_PERIOD=300`, the next Update comes after 5 minutes, and most
carriers have rebound the UDP mapping by then. To force it sooner, detach
and reattach the bearer (A: `AT+CFUN=4`, then `AT+CFUN=1` after a few
seconds; the modem keeps the DTLS socket context, so CID carries over. If
your mfw drops sockets on CFUN=4, use the idle method instead).

lwm2md:
```
updated <EP> addr=<new-ip-or-port> lifetime=1h0m0s
awake <EP>
```
Device: `<inf> net_lwm2m_rd_client: Update Done`.

- **Pass:** all of these hold:
  1. The `updated` line shows a different `addr=` than the previous
     `registered`/`updated` line.
  2. In pcap, the first record from the new `ip.src:udp.srcport` has
     content_type 25 and **no handshake records (22) follow it**.
  3. The Update gets 2.04, and no new `registered <EP>` line appears. A
     new registration means the session or registration was lost.
- **Fail:** a new ClientHello (`handshake.type 1`) from the new port, a
  new `registered <EP>` line, or the device logging
  `Failed with code 4.4` (it re-registers).

### 2.4 DTLS session resumption (session caching)

Restart the LwM2M client without rebooting the modem or board. On B:
`lwm2m stop`, then `lwm2m start <EP>`. On A: the sample's
`lwm2m stop/start` shell, or `AT+CFUN=4/1` when the socket is closed.
- **Pass:** the pcap handshake has ClientHello (1) with a non-empty
  session id, then ServerHello (2) with the **same** session id,
  ChangeCipherSpec and Finished, and **no ClientKeyExchange (16)**.
- **Fail:** a 16 in that handshake means a full handshake. lwm2md keeps
  sessions for 24 h (SEC-11), so check the client's session cache.

### 2.5 Queue mode wake

After the device logs that its RX window closed (B: state
`ENGINE_REGISTRATION_DONE_RX_OFF`; A: `LwM2M queue mode RX window closed`
or similar), the REST call below blocks. compat never answers "delayed";
it waits for the device:

```sh
time curl -s "localhost:8080/api/clients/$EP/3/0/0?format=TEXT&timeout=600"
```
- **Pass:** the call returns `{"status":"CONTENT(205)",...,"value":"<manufacturer>"}`
  within about a second of the next `updated <EP>` and `awake <EP>` lines
  (at most `UPDATE_PERIOD` seconds). No 4.04 or timeout on the device.
- **Fail:** HTTP 504 `Request timeout`, which means no Update arrived
  within 600 s. If the device logs `Update Done` and still no request
  reaches it, the downlink did not cross the NAT: check that 2.3 passes.

### 2.6 FOTA pull via Block2

```sh
curl -s -X POST "localhost:8080/api/clients/$EP/5/0/3/observe?format=TEXT&timeout=60"   # state
curl -s -X PUT "localhost:8080/api/clients/$EP/5/0/1?format=TEXT&timeout=60" -H 'content-type: application/json' \
  -d '{"id":1,"kind":"singleResource","type":"string","value":"coaps://<public-ip>:5694/app.bin"}'
```
The device pulls `/app.bin` from the firmware listener over DTLS with the
same PSK. The firmware listener uses the LwM2M server's security store.
Plain `coap://<public-ip>:5693/app.bin` also works.

- Device (B): `<inf> net_lwm2m_pull_context: Connecting to server coaps://...:5694/app.bin`,
  then state notifications.
- Event stream: `NOTIFICATION` with `/5/0/3` values 1 (Downloading), then
  2 (Downloaded).
- pcap on 5694: a handshake (or a resumption), then CoAP GETs with Block2
  `NUM` 0,1,2,... and one response per block. Each response carries Size2
  and the same ETag.
- Then execute Update: `curl -s -X POST localhost:8080/api/clients/$EP/5/0/2`.
  The state goes to 3 (Updating), the device reboots, re-registers, and
  `/5/0/5` (Update Result) reads `1`.
- **Pass:** `/5/0/3` goes 0→1→2, the execute returns `CHANGED(204)`,
  the device re-registers, and
  `curl "localhost:8080/api/clients/$EP/5/0/5?format=TEXT"` shows value
  `"1"`. The image hash the device reports (B: `mcumgr image list`; A:
  the new app version in `/3/0/3`) matches `fw/app.bin`.
- **Fail:** `/5/0/5` = 5/6/7 (CRC, unsupported package, out of memory) or
  9 (invalid URI). An `Unexpected response from server: 4.4` on the
  device means the path is wrong: the URI path must be the file name in
  `-fw-dir`. A transfer that stalls at a fixed block means a NAT/MTU
  problem: lower the block size (`CONFIG_LWM2M_COAP_BLOCK_SIZE=256`). The
  file server honours the client's SZX.

---

## 3. Report template

| Check | A: nRF91xx | B: mbedTLS | Evidence (log line / pcap frame) |
|---|---|---|---|
| 2.1 bootstrap + register | | | |
| 2.2 CID negotiated | | | |
| 2.3 NAT rebind, no handshake | | | |
| 2.4 session resumption | | | |
| 2.5 queue-mode wake | | | |
| 2.6 FOTA pull Block2 | | | |

Attach `lwm2md.log`, `dev.pcapng` and the device console log.
