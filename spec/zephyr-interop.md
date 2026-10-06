# Zephyr LwM2M interop suite: inventory and Leshan REST contract

Source: Zephyr `74b7173e9c929cf8eed570fb3df099c5514720c0`, `tests/net/lib/lwm2m/interop/`.
Leshan reference: clone at `22bc753133829b91aa1f090e1e1179e6957940a0`.

Path prefixes used in citations:
- `I/` = `zephyr/tests/net/lib/lwm2m/interop/`
- `L/` = leshan clone root
- `CS` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/ClientServlet.java`
- `ES` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/EventServlet.java`
- `NS` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/json/JacksonLwM2mNodeSerializer.java`
- `ND` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/json/JacksonLwM2mNodeDeserializer.java`
- `RS` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/json/JacksonResponseSerializer.java`
- `RG` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/json/JacksonRegistrationSerializer.java`
- `LK` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/json/JacksonLinkSerializer.java`
- `SS` = `L/leshan-demo-servers-shared/src/main/java/org/eclipse/leshan/demo/servers/json/servlet/SecurityServlet.java`
- `SD` = `L/leshan-demo-servers-shared/src/main/java/org/eclipse/leshan/demo/servers/json/JacksonSecurityDeserializer.java`
- `BS` = `L/leshan-demo-bsserver/src/main/java/org/eclipse/leshan/demo/bsserver/servlet/BootstrapServlet.java`
- `BC` = `L/leshan-lwm2m-bsserver/src/main/java/org/eclipse/leshan/bsserver/BootstrapConfig.java`
- `QH` = `L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/servlet/queuemode/QueueHandler.java`

Files in the suite (all read): `tests.yaml`, `CMakeLists.txt`, `requirements.txt`, `prj.conf`,
`docker-test.sh`, `README.md`, `osv-scanner.toml`, `boards/native_sim.conf`, `boards/qemu_x86.conf`,
`pytest/{pytest.ini,conftest.py,leshan.py,test_blockwise.py,test_bootstrap.py,test_lwm2m.py,test_nosec.py,test_observe_attributes.py,test_portfolio.py}`,
`src/lwm2m-client.c`, `src/firmware_update.c`.

Not in the sparse clone, so not covered here: `/net-tools/start-leshan.sh` (in the zephyrproject-rtos/net-tools
Docker image), `scripts/net/run-sample-tests.sh`, and the `twister_harness` (`Shell`, `DeviceAdapter`) sources.

---

## 1. How the harness works

### 1.1 Twister / pytest wiring
- Twister test `net.lwm2m.interop`: `harness: pytest`, `timeout: 600`, `slow: true`, `pytest_dut_scope: module`, platforms `native_sim` (integration) and `qemu_x86` (`I/tests.yaml:1-16`).
  - `pytest_dut_scope: module` means the DUT (Zephyr binary) is restarted for every test module, so every module bootstraps and registers again with a fresh random endpoint.
- Run: `twister -p native_sim -vv --enable-slow -T tests/net/lib/lwm2m/interop` (`I/README.md:107-109`). Docker variant: `./scripts/net/run-sample-tests.sh tests/net/lib/lwm2m/interop` (`I/README.md:113-115`).
- `slow` pytest marker is declared in `I/pytest/pytest.ini:1-3`.
- Extra Python dependency: `CoAPthon3>=1.0.2` (`I/requirements.txt:1`), used only by int-105. A known vuln in it is ignored (`I/osv-scanner.toml:1-3`).
- Test modules run in alphabetical order: blockwise, bootstrap, lwm2m, nosec, observe_attributes, portfolio. Each module gets a fresh DUT.

### 1.2 Network / Leshan startup
- Host (Leshan) is `192.0.2.2`, DUT is `192.0.2.1`, gateway `192.0.2.2`. IPv4 only, no DHCP (`I/prj.conf:4-15`, `I/README.md:12-13`).
- The Docker path (`I/docker-test.sh:10-20`) starts the `net-tools` container with `--ip=192.0.2.2 --ip6=2001:db8::2 -p 8080:8080 -p 8081:8081 -p 5683:5683/udp` and then runs `/net-tools/start-leshan.sh`, followed by `twister -p native_sim -T ./ --enable-slow -vv`.
- Manual docker (`I/README.md:24-48`): `docker build -t net-tools .`, `net-setup.sh --config docker.conf start`, `docker run --hostname=net-tools --name=net-tools --ip=192.0.2.2 --ip6=2001:db8::2 -p 8080:8080 -p 8081:8081 -p 5683:5683/udp --rm -dit --network=net-tools0 net-tools`, then `docker container exec net-tools /net-tools/start-leshan.sh`.
- Manual Leshan (`I/README.md:50-98`):
  - LwM2M server: `java -jar ./leshan-server-demo.jar -wp 8080 -vv [--models-folder objects]`, giving CoAP udp/5683, CoAPS udp/5684, and REST on tcp/8080.
  - Bootstrap server: `java -jar ./leshan-bsserver-demo.jar -lp=5783 -slp=5784 -wp 8081 [-vv]`, giving CoAP udp/5783, CoAPS udp/5784, and REST on tcp/8081.
  - Port list: `I/README.md:55-60`. The README lists DTLS bootstrap as "5684", which is a typo. The harness uses 5784 (`I/pytest/conftest.py:26`).
- Leshan demo DTLS CID defaults to `on` (6 bytes) (`L/leshan-demo-servers-shared/src/main/java/org/eclipse/leshan/demo/servers/cli/DtlsSection.java:27-38`). The DUT enables CID.

### 1.3 Ports/addresses used by the harness (`I/pytest/conftest.py:23-34`)
| Constant / option | Default | Used for |
|---|---|---|
| `LESHAN_IP` / `--leshan_addr` | `192.0.2.2` | Server URIs written into the DUT; CoAPthon helper target |
| `COAP_PORT` | 5683 | nosec registration; int-105 fake deregister |
| `COAPS_PORT` | 5684 | DTLS-PSK LwM2M server URI handed out by bootstrap |
| `BOOTSTRAP_COAPS_PORT` | 5784 | DTLS-PSK bootstrap URI written to DUT `/0/0/0` |
| `--leshan_rest_api` | `http://localhost:8080/api` | `leshan` fixture (LwM2M server REST) |
| `--leshan_bootstrap_rest_api` | `http://localhost:8081/api` | `leshan_bootstrap` fixture (BS server REST) |
| `--passwd` | `''` | If set, used as both BS PSK and server PSK instead of random |

Non-secure bootstrap (udp/5783) is never used by the tests.

### 1.4 DUT security modes, identities, keys
- **NoSec** (`endpoint_nosec`, `I/pytest/conftest.py:96-124`), shell commands:
  `lwm2m write 0/0/0 -s coap://192.0.2.2:5683`, `0/0/1 -b 0` (not BS), `0/0/2 -u8 3` (NoSec), `0/0/3 -s <ep>`, `lwm2m create 1/0`, `0/0/10 -u16 1` (SSID), `1/0/0 -u16 1`, `1/0/1 -u32 86400` (lifetime), `lwm2m start <ep> -b 0`. Then it waits for `.*Registration Done` (5 s). Teardown: `lwm2m stop` and wait for `.*Deregistration success` (10 s).
  - No security info is created in Leshan for this endpoint. The comment at `I/pytest/conftest.py:166-167` says Leshan rejects non-secure connections for endpoints that have PSK info. Our server must do the same: NoSec register is accepted only when no security info exists for the endpoint.
- **PSK via bootstrap** (`endpoint_bootstrap`, `I/pytest/conftest.py:126-169`):
  - Endpoint name: `'client_' + hex(os.urandom(1))`, e.g. `client_a3` (`:132`). The same scheme is used for nosec (`:103`).
  - BS PSK and server PSK are each 16 random lowercase ASCII letters, or `--passwd` (`:133-138`).
  - PSK identity = endpoint name, for both servers.
  - PSK key bytes = the raw ASCII bytes of the password. The REST API carries them hex-encoded (`binascii.b2a_hex(passwd.encode())`) (`I/pytest/leshan.py:372-373,379-380`). Inside the bootstrap config they are carried as JSON byte arrays (`I/pytest/leshan.py:382-384`).
  - Sequence:
    1. `leshan_bootstrap.create_bs_device(ep, 'coaps://192.0.2.2:5684', bs_passwd, passwd)`
    2. `leshan.create_psk_device(ep, passwd)` (`:145-147`)
    3. sleep 2 s
    4. DUT: `0/0/0 -s coaps://192.0.2.2:5784`, `0/0/1 -b 1` (is BS), `0/0/2 -u8 0` (PSK), `0/0/3 -s <ep>`, `0/0/5 -s <bs_passwd>`, `lwm2m start <ep> -b 1` (`:153-158`)
  - Teardown: `lwm2m stop` and wait for `Deregistration success`. A `finally` block then runs `leshan.delete_device(ep)` and `leshan_bootstrap.delete_bs_device(ep)` (`:161-169`). `delete_bs_device` raises if `DELETE /bootstrap/<ep>` returns 404 (see §3).
- DUT TLS stack: mbedTLS + PSA, DTLS 1.2, PSK key exchange only, PSA AES + CCM + SHA-256 + TLS12 PRF + PSK-to-MS. In practice this means `TLS_PSK_WITH_AES_128_CCM_8`/`CCM`. Max ciphersuites 3. DTLS CID is enabled (`I/prj.conf:33-34,72-98`, `I/src/lwm2m-client.c:62-89`). The DTLS handshake timeout is shortened to 100-500 ms (`I/src/lwm2m-client.c:77-86`). Only one DTLS context is allowed (`I/prj.conf:97`).
- DUT config of note (`I/prj.conf`):
  - LwM2M 1.1 (`:32`).
  - Content formats: SenML-JSON, SenML-CBOR, legacy JSON, TLV (`:37-49`). Plain text and opaque are built in.
  - Queue mode on with `QUEUE_MODE_UPTIME=20` s, `UPDATE_PERIOD=30`, `SECONDS_TO_UPDATE_EARLY=1`, stop polling at idle (`:60-65`).
  - Default lifetime 30, pmin 1, pmax 10. This is OMA ETS "Configuration 3" (`:67-70`).
  - CoAP ACK timeout 1000 ms, not randomised, RD max retries 2 (`:55-58`).
  - Block size 512, max message 1163, block1 contexts 2, output block contexts 2 (`:103-108`).
  - Max observers 5 (`:114`), 20 attributes (`:118`).
  - Extended CoAP option length 40, for a long location path (`:51-53`).
  - Engine max pending 2, replies 2, messages 3 (`:111-113`).
  - `native_sim`: DNS server 192.0.2.2, LwM2M DNS support, ASAN, real-time slowdown (`I/boards/native_sim.conf:1-10`).

### 1.5 DUT application objects (`I/src/lwm2m-client.c`, `I/src/firmware_update.c`)
- `/3/0`, all read-only: `0`="Zephyr", `1`="client-1", `2`="serial-1", `3`="1.2.3" (`lwm2m-client.c:25-28,107-114`); `4` exec reboot callback (`:115`); `17`=CONFIG_BOARD (`:116`).
- `/3/0/6/{0,1}` = {1 (internal battery), 5 (USB)}; `/3/0/7/{0,1}` = {3800, 5000} mV; `/3/0/8/{0,1}` = {125, 900} mA (`:36-41,119-131`).
- Reboot exec (`/3/0/4`) logs `DEVICE: REBOOT` and calls `lwm2m_rd_client_stop` without deregistering, so the client does not deregister (`:43-60`).
- `/19` BinaryAppData: on create, `/19/x/0/0` gets a 4096-byte buffer and `/19/x/3` a 16-byte buffer (`:91-101,133`).
- `/5/0` Firmware:
  - Pre-write buffer is 64 bytes. Every block updates a CRC32 (IEEE) and sleeps 100 ms per block, which forces block-wise timeouts (`firmware_update.c:16-55`).
  - Exec `/5/0/2` logs `UPDATE, (CRC <u32>)` and sets state idle / result success (`:22-30`).
  - `/5/0/8/0` is the supported-protocol resource instance (`:73-76`).
- RD events are logged with strings the tests grep: `Registration Done`, `Update Done`, `Deregistration success`, `Bootstrap transfer complete`, `Server Initiated Bootstrap`, `Queue mode RX window closed`, `Registration update complete`, `LwM2M server disabled`, `Disconnected`, `Observer added/removed for <path>`, `SEND status: 0`, `Failed with code 4.4`. Most of these come from the Zephyr LwM2M engine. App-side ones are in `lwm2m-client.c:136-233`.
- A network error makes the app stop the RD client (`:193-196`). A notify timeout triggers a registration update (`:226-231`).

### 1.6 Fixtures (`I/pytest/conftest.py`)
| Fixture | Scope | Behaviour | Line |
|---|---|---|---|
| `leshan` | session | `Leshan(--leshan_rest_api)`. Skips the whole session if construction raises `RuntimeError` (server down) | 55-66 |
| `leshan_bootstrap` | session | `Leshan(--leshan_bootstrap_rest_api)`, same skip logic | 68-79 |
| `helperclient` | module | CoAPthon3 `HelperClient(server=(leshan_addr, 5683))`. Skips if the package is missing | 81-93 |
| `endpoint_nosec` | module | NoSec registration (see §1.4) | 96-124 |
| `endpoint_bootstrap` | module | Creates PSK + BS config, starts the DUT with bootstrap (see §1.4) | 126-169 |
| `endpoint_registered` | module | Waits for `.*Registration Done` (5 s) once, then marks `bootstrap=registered=True` | 171-178 |
| `endpoint` | function | `endpoint_registered.check_update()`: if registered and more than 5 s since the last check, runs shell `lwm2m update`. This keeps the queue-mode client awake | 180-184, 44-49 |
| `configuration_C13` | module (function for int-1635 via indirect parametrize) | Shell creates `/16/0` with res-instances `0/0..3` = `"Host Device ID #1"`, `"Host Device Manufacturer #1"`, `"Host Device Model #1"`, `"Host Device Software Version #1"`. Teardown: `lwm2m delete /16/0` | 186-199 |

`Endpoint.__str__` returns the name, so f-strings in URLs use the bare endpoint name (`I/pytest/conftest.py:51-52`).

### 1.7 Timeouts
- `Leshan.timeout = 10` (s) by default (`I/pytest/leshan.py:25`). Used as the HTTP client timeout and sent as `?timeout=10` (Leshan converts seconds to ms, `CS:787-801`). If the param is absent or invalid, Leshan uses 5000 ms (`CS:124`).
- Tests override it: blockwise uses 600 or 1 (`I/pytest/test_blockwise.py:32,54,62,90,121`). Event streams use 30 or 50 (see tables).
- DUT log waits are mostly 5 s. int-107 waits up to `lifetime`=120 s, int-109 120 s, int-7 600 s.
- Requests with no `timeout` query param fall back to Leshan's 5 s default: `execute`, `write_attributes`, `remove_attributes`, `discover`, `delete`, `cancel_*`, `observe`. Strictly, `observe` does send `timeout` (it passes `data=""`, see §3).

---

## 2. Test inventory (66 test functions)

Default `leshan.format` is `SENML_CBOR` (`I/pytest/leshan.py:27`). The "REST" column uses the helper names from §3.
"shell" = Zephyr shell command on the DUT. "log" = `dut.readlines_until` regex.

### 2.1 `test_blockwise.py` (fixture `endpoint`, PSK)
| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `test_blockwise_1` (`:25-44`) | none (block-wise) | format=OPAQUE, timeout=600. Writes 5000 B (`b'1234567890'*500`) to `/5/0/0` (block1 PUT), executes `/5/0/2`, log `app_fw_update: UPDATE` | `len(lines)>0`; `crc == zlib.crc32(fw)` from DUT log | `write`(PUT single opaque), `execute` |
| `test_blockwise_2` (`:46-74`) | none | Same, but first write uses timeout=1 so Leshan aborts mid-transfer. The exception is caught, then shell `lwm2m update`, sleep 1. Rewrites with timeout=600, then exec `/5/0/2` | Same CRC assertions. The server must abandon/cancel a timed-out block1 transfer so a new one can start | `write`×2, `execute` |
| `test_blockwise_3` (`:77-107`) | none | shell `lwm2m create /19/0`, log `Update Done`. Writes 4096 random ASCII letters to `/19/0/0/0` as OPAQUE (resourceInstance). Shell `lwm2m read /19/0/0/0 -crc32`. Reads `/19/0/0` with TLV and with SENML_CBOR (block2 GET) | shell crc == `zlib.crc32(data)`; for each fmt, `crc == crc32(unhex(read[0][0]))` (multiResource → `{0: hex}`) | `write`(resourceInstance opaque), `read`×2 |
| `test_blockwise_4` (`:109-137`) | none | Create `/19/0`, write 4096 B OPAQUE to `/19/0/0/0`. format=SENML_CBOR. Opens event stream, shell `lwm2m send /19/0`, log `SEND status: 0`, then `next_event('SEND')` (block1 POST /dp) | `send is not None`; `crc32(unhex(send[19][0][0][0])) == crc32(data)` | `write`, `get_event_stream`/SEND |

Leaks in the blockwise tests: `test_blockwise_3` restores `leshan.format` to the loop variable (`'SENML_CBOR'`), not the original (`:99-105`), which only works because the original was `SENML_CBOR`. `test_blockwise_1`/`2` never delete written firmware.

### 2.2 `test_bootstrap.py`
| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `test_LightweightM2M_1_1_int_1` (`:39-43`, fixture `endpoint_bootstrap`) | int-1 (also covers int-0 `verify_…_int_0` `:34-37`, int-101 `verify_…_int_101` `:90-94`, int-401 `verify_…_int_401` `:96-105`) | Waits for `.*Bootstrap transfer complete` (5 s), then `.*Registration Done` (5 s). Shell reads `0/0/0 -s` and `0/0/2 -u8` | `leshan.get('/clients/<ep>')` truthy; `'coaps://' in /0/0/0`; `/0/0/2 == 0`; `GET /clients/<ep>` → `resp["secure"]` truthy | `get('/clients/<ep>')`×2 |
| `…_int_4` (`:45-56`) Bootstrap Delete | int-4 | shell `lwm2m create 1/2`, `lwm2m read 1/2/0`, `retval`. Executes `/1/0/9` (Bootstrap-Request trigger). Log `Registration Done` | `retval == 0` before; `retval < 0` after re-bootstrap (`/1/2` deleted by BS `toDelete:["/0","/1"]`) | `execute('1/0/9')` |
| `…_int_5` (`:58-63`) Server Initiated Bootstrap | int-5 | Exec `/1/0/9`. Logs `Server Initiated Bootstrap` (1 s), `Bootstrap transfer complete` (5 s), `Registration Done` (5 s) | log waits only | `execute` |
| `…_int_6` (`:65-78`) Bootstrap Sequence | int-6 | `lwm2m stop`, `Deregistration success`, `lwm2m start <ep>`, `Registration Done`. Then `stop`, delete `1/0` and `0/1` locally, `start`, `Registration Done` | first start: no line contains "Bootstrap"; second: some line contains "Bootstrap" | none |
| `…_int_7` (`:80-88`, `@slow`) Fallback to bootstrap | int-7 | stop. Writes `0/1/0 -s coaps://10.10.10.10:5684` (unreachable server), start, waits up to 600 s for `Registration Done` | some line contains "Bootstrap" (client fell back to BS, which rewrites `/0/1`) | none |

### 2.3 `test_lwm2m.py` (fixture `endpoint`, PSK, registered via bootstrap)
Helpers:
- `verify_device_object(resp)` (`:99-106`): `resp[0][0]=='Zephyr'`, `[0][1]=='client-1'`, `[0][2]=='serial-1'`, `[0][3]=='1.2.3'`, `[0][11][0]==0`, `[0][16]=='U'`.
- `verify_server_object(obj)` (`:108-116`): `obj[0][0]==1`, `[0][1]==86400`, `[0][2]==1`, `[0][3]==10`, `[0][5]==86400`, `[0][6] is False`, `[0][7]=='U'`.
- `verify_setting_basic_in_format(fmt)` (`:178-203`):
  1. Reads `/1/0` and verifies the server object.
  2. Drops RO resources 0 and 13 from the copy.
  3. `update_obj_instance('1/0', {2:101,3:1010,5:2000,6:True,7:'U'})` → status `CHANGED(204)`.
  4. Reads `/1/0` and checks those 5 values.
  5. `replace_obj_instance('1/0', saved_copy)` → `CHANGED(204)`.
  6. Re-reads and verifies the server object again.
- `query_basic_in_senml(fmt)` (`:335-343`): `verify_server_object(read('1')[1])`, `verify_device_object(read('3/0'))`, `read('3/0/16')=='U'`, `read('3/0/11/0')==0`.
- `setting_basic_senml(fmt)` (`:353-372`):
  1. `update_obj_instance('1/0',{1:61,6:True})` → `CHANGED(204)`.
  2. Read `/1/0`: `[0][1]==61`, `[0][6] is True`.
  3. `write('16/0/0/0','test_value')` → `CHANGED(204)`.
  4. `read('16')[16][0][0][0]=='test_value'`.
  5. `write('1/0/1',63)` → `CHANGED(204)`; `read('1/0/1')==63`.
  6. Shell restores `/1/0/1=86400`, `/1/0/6=0`.

| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `int_102` (`:33-45`) Registration Update | int-102 | shell read `1/0/1`, write lifetime+10 via REST, log `Update Done` | `latest["lastUpdate"] > start_ms`; `<= now_ms`; `latest["lifetime"] == lifetime` | `write('1/0/1', int)`, `get('/clients/<ep>')` |
| `int_103` (`:47-56`) Deregistration | int-103 | exec `/1/0/4` (Disable), logs `LwM2M server disabled`, `Deregistration success`. Shell stop, sleep 1, `start <ep>`, `Registration Done` | log waits | `execute('1/0/4')` |
| `int_104` (`:58-63`) Registration Update Trigger | int-104 | shell `lwm2m update` → `Update Done`; exec `/1/0/8` → `Update Done` | log waits | `execute('1/0/8')` |
| `int_107` (`:65-75`, `@slow`) Extending lifetime | int-107 | REST write `/1/0/1`=120, `Update Done`, shell read. Waits up to 120 s for the next `Update Done` | shell lifetime==120; `get('/clients/<ep>')` truthy | `write`, `get` |
| `int_108` (`:77-79`) Turn on Queue Mode | int-108 | none | `get('/clients/<ep>')["queuemode"]` truthy | `get` |
| `int_109` (`:81-88`, `@slow`) Behavior in Queue Mode | int-109 | Waits for `Queue mode RX window closed` (120 s). Shell write `1/0/1 86400`, waits for `Registration update complete` (10 s) | log waits | none |
| `int_201` (`:90-97`) | int-201 Plain Text | format=TEXT. Reads `3/0/0`,`3/0/1`,`3/0/2` | `=='Zephyr'`, `'client-1'`, `'serial-1'` | `read`×3 |
| `int_203` (`:118-124`) | int-203 TLV | format=TLV, read `3/0` | `verify_device_object` | `read` |
| `int_204` (`:126-132`) | int-204 JSON | format=JSON (legacy OMA JSON), read `3/0` | `verify_device_object` | `read` |
| `int_205` (`:134-150`) | int-205 Set Plain Text | format=TEXT. Writes `1/0/2`=101, `1/0/3`=1010, `1/0/5`=2000, reads them back. Then writes 1, 10, 86400 and reads back | each read equals the written int | `write`×6, `read`×6 |
| `int_211` (`:152-161`) | int-211 CBOR | format=CBOR. Shell read `1/0/0 -u16` | `read('1/0/0')==short_id`; `read('1/0/6') is False`; `read('1/0/7')=='U'` | `read`×3 |
| `int_212` (`:163-176`) | int-212 Set CBOR | format=CBOR. Writes `1/0/2`=101, `1/0/3`=1010, `1/0/6`=True, reads back, then restores 1, 10, False | `==101`, `==1010`, `is True` | `write`×6, `read`×3 |
| `int_215` (`:205-207`) | int-215 Set TLV | `verify_setting_basic_in_format('TLV')` | see helper | `read`, `update_obj_instance`, `replace_obj_instance` |
| `int_220` (`:209-211`) | int-220 Set JSON | Same with `'JSON'` | see helper | same |
| `int_221` (`:213-217`) | int-221 Ops on Security | none | `read('0/0')['status']=='UNAUTHORIZED(401)'`; `write('0/0/0','coap://localhost')['status']=='UNAUTHORIZED(401)'`; `write_attributes('0',{'pmin':10})['status']=='UNAUTHORIZED(401)'` | `read`, `write`, `write_attributes` |
| `int_222` (`:219-228`) | int-222 Read Object | Read `1` and `3` | `len(read('1'))==1`; `len(r[1][0])==11`; `len(read('3'))==1`; `len(r[3])==1`; `len(r[3][0])==15`; `r[3][0][0]=='Zephyr'` | `read`×2 |
| `int_223` (`:230-236`) | int-223 Read Instance | Read `1/0`, `3/0` | `len(r[0])==11`; `len(r[0])==15`; `r[0][0]=='Zephyr'` | `read`×2 |
| `int_224` (`:238-243`) | int-224 Read Resource | none | `1/0/0==1`, `1/0/1==86400`, `1/0/6 is False`, `1/0/7=='U'` | `read`×4 |
| `int_225` (`:245-247`) | int-225 Read Res Instance | none | `read('3/0/11/0')==0` | `read` |
| `int_226` (`:249-264`) | int-226 Partial Update | Shell read lifetime. `update_obj_instance('1/0',{1:60,6:True})`, read back, then restore | both updates `['status']=='CHANGED(204)'`; `1/0/1==60`; `1/0/6 is True` | `update_obj_instance`×2, `read`×2 |
| `int_227` (`:266-275`) | int-227 Write replace Resource | Writes `1/0/1`=63, log `Update Done` | write status `CHANGED(204)`; `get(/clients/ep)["lifetime"]==63`; `read('1/0/1')==63`; restore write status `CHANGED(204)` | `write`×2, `get`, `read` |
| `int_228` (`:277-285`) | int-228 Write Res Instance | `create_obj_instance('16/0',{0:{0:'a',1:'b'}})`, log `Update Done`, writes `16/0/0/0`='test' | create status `CREATED(201)`; write `CHANGED(204)`; `read('16/0/0/0')=='test'` | `create_obj_instance`, `write`, `read` |
| `int_229` (`:287-304`) | int-229 Read-Composite | For SENML_JSON and SENML_CBOR: `composite_read(['/3','1/0'])`, then `composite_read(['1/0/1','/3/0/11/0'])` | first: `len(keys)==2`, `r[3]` and `r[1][0]` not None, `len(r[3][0])==15`, `len(r[1][0])==11`; second: `len==2`, `r[1][0][1]` and `r[3][0][11][0]` not None | `composite_read`×4 |
| `int_230` (`:306-333`) | int-230 Write-Composite | For SENML_JSON and SENML_CBOR: `composite_write({"/1/0/1":60,"/1/0/6":True,"/16/0/0":{"0":"aa","1":"bb","2":"cc","3":"dd"}})`. Reads `1/0` and `16/0/0`. Shell restores | status `CHANGED(204)`; `r[0][1]==60`; `r[0][6] is True`; `read('16/0/0')` → `r[0][0..3]=="aa","bb","cc","dd"`. **Note**: reading a multiResource via `read()` returns `{rid: {riid: v}}`, so `resp[0]` here is the `{0: {...}}` key (rid=0) | `composite_write`, `read`×2 |
| `int_231` (`:345-347`) | int-231 SenML JSON | `query_basic_in_senml('SENML_JSON')` | see helper | `read`×4 |
| `int_232` (`:349-351`) | int-232 SenML CBOR | `query_basic_in_senml('SENML_CBOR')` | see helper | `read`×4 |
| `int_233` (`:374-376`) | int-233 Set SenML CBOR | `setting_basic_senml('SENML_CBOR')` | see helper | `update_obj_instance`, `read`, `write` |
| `int_234` (`:378-380`) | int-234 Set SenML JSON | `setting_basic_senml('SENML_JSON')` | see helper | same |
| `int_235` (`:382-387`) | int-235 Composite on root | `composite_read(['/'])` (SENML_CBOR) | keys 1, 3, 5 all present in decoded dict. Leshan returns `content: {"/": {"kind":"root","objects":[…]}}` | `composite_read` |
| `int_236` (`:389-394`) | int-236 Partial Presence | `composite_read(['1/0','/3/0/11/0','/3339/0/5522','/3353/0/6030'])` (objects 3339 and 3353 do not exist on the DUT) | `r[1][0][1]` not None; `r[3][0][11][0]` not None; `len(r)==2`. **The missing paths must be absent from `content` (not `null`)**, otherwise `parse_composite` crashes | `composite_read` |
| `int_237` (`:396-402`) | int-237 Read without Content-Type | `leshan.format=None`, so the GET has no `format` param and Leshan sends no Accept | `read('1')[1][0][1]` not None; `read('3')[3][0][0]=='Zephyr'` | `read`×2 |
| `int_241` (`:404-411`) | int-241 Reboot | exec `/3/0/4`. Logs `DEVICE: REBOOT`, `rd_client_event: Disconnected`. Shell `lwm2m start <ep> -b 0`, `Registration Done` | `get('/clients/<ep>')` truthy | `execute`, `get` |
| `int_256` (`:413-418`) | int-256 Write Failure | Shell read short id. Writes `1/0/0`=123 (RO) | write `['status']=='METHOD_NOT_ALLOWED(405)'`; `read('1/0/0')==short_id` | `write`, `read` |
| `int_257` (`:420-439`) | int-257 Write-Composite | SENML_JSON and SENML_CBOR: `composite_write({"/1/0/2":102,"/1/0/6":True,"/3/0/13":datetime.fromtimestamp(0)})` (type `time`, value `0`). Shell restores | status `CHANGED(204)`; `1/0/2==102`; `1/0/6 is True` | `composite_write`, `read`×2 |
| `int_260` (`:441-467`) | int-260 Discover | `discover('3')`. `write_attributes('3',{pmin:10,pmax:200})`. `discover('3/0')`. `write_attributes('3/0/7',{lt:1,gt:6,st:1})`. `discover('3/0')`, `discover('3/0/7')`. Restore with `remove_attributes('3',['pmin','pmax'])` | discover `3` contains `/3,/3/0,/3/0/1,/3/0/2,/3/0/3,/3/0/4,/3/0/6,/3/0/7,/3/0/8,/3/0/9,/3/0/11,/3/0/16`; write-attrs `CHANGED(204)`; `int(r['/3/0/{6,7,8}']['dim'])==2`; 2nd write-attrs `CHANGED(204)`; discover `3/0` contains same set minus `/3`; `/3/0/7` dim 2, `float(lt)==1.0`, `float(gt)==6.0`, `float(st)==1.0`; discover `3/0/7` keys exactly `{/3/0/7,/3/0/7/0,/3/0/7/1}` | `discover`×4, `write_attributes`×2, `remove_attributes` |
| `int_261` (`:469-490`) | int-261 Write-Attr multi-res | `discover('3/0/11')`. write_attributes on `3` {pmin:10,pmax:200}, `3/0` {pmax:320}, `3/0/11/0` {pmax:100,epmin:1,epmax:20}. Discover again. Restores pmin/pmax on 3, pmax on 3/0, pmax on 3/0/11/0 | first discover keys exactly `{/3/0/11,/3/0/11/0}`, `dim==1`; 3 write-attrs `CHANGED(204)`; then `int(r['/3/0/11']['pmin'])==10`, `int(...['pmax'])==320` (inherited attrs reported by the DUT), `int(r['/3/0/11/0']['pmax'])==100` | `discover`×2, `write_attributes`×3, `remove_attributes`×3 |
| `int_280` (`:493-505`) | int-280 Read-Composite OK | `composite_read(['/3/0/16','/3/0/11/0','/1/0'])` | `len(r)==2`; `len(r[3])==1`; `len(r[3][0])==2`; `r[3][0][11][0]==0`; `r[3][0][16]=='U'`; `r[1][0][0]==1`; `r[1][0][1]==86400`; `r[1][0][6] is False`; `r[1][0][7]=='U'` | `composite_read` |
| `int_281` (`:507-513`) | int-281 Partial Read-Composite | `composite_read(['/1/0/1','/1/0/7','/1/0/8'])` (8 is executable, so no value) | `len(r)==1`; `len(r[1][0])==2`; `r[1][0][1]==86400`; `r[1][0][7]=='U'` (absent path omitted) | `composite_read` |
| `int_301` (`:519-545`, `@slow`) | int-301 Observe/Notify | Reads `3/0/6`. write_attributes `3/0/7` {pmin:5,pmax:10}. `observe('3/0/7')`. Event stream (timeout 30). Shell writes `/3/0/7/0` 3000, then 3500 | `r[6][0]==1`, `r[6][1]==5`; write-attrs `CHANGED(204)`; notif not None and `[3][0][7][0]==3000`; next notif `==3500` arriving `start+5 ± 0.5 s` (pmin); next notif `==3500` at `>= start+15-1` (pmax). Then `cancel_observe` (active), `remove_attributes` | `read`, `write_attributes`, `observe`, SSE NOTIFICATION, `cancel_observe`, `remove_attributes` |
| `int_302` (`:547-564`) | int-302 Cancel via Reset | `observe` `3/0/7` and `3/0/8`. Stream: shell write `/3/0/7/0` 4000, notif. `passive_cancel_observe('3/0/7')`, shell write 3000, log `Observer removed for 3/0/7`. Stream: write `/3/0/8/0` 100, notif. Passive cancel `3/0/8`, write 50, log `Observer removed for 3/0/8` | `[3][0][7][0]==4000`; `[3][0][8][0]==100`. **The server must answer the next notification for a forgotten observation with CoAP RST** | `observe`×2, SSE, `passive_cancel_observe`×2 |
| `int_303` (`:566-581`) | int-303 Cancel with Observe=1 | Same flow but uses `cancel_observe` (active, `?active`) and expects `Observer removed` without another write | `4000`, `100` | `observe`×2, SSE, `cancel_observe`×2 |
| `int_304` (`:583-613`, `@slow`) | int-304 Observe-Composite | Shell sets `1/0/2`=0, `1/0/3`=0 (Configuration C.1). write_attributes `1/0/1` {pmin:30,pmax:45}. `composite_observe(['/1/0/1','/3/0/11/0','/3/0/16'])`. Stream (50 s), next NOTIFICATION | initial response and notif both: `[1][0][1]` not None, `[3][0][11][0]` not None, `[3][0][16]=='U'`, `len==2`, `len([1])==1`, `len([3][0])==2`; notif `start+30 < now` and `start+45 > now-1`. Then `cancel_composite_observe`, restore C.3, `remove_attributes` | `write_attributes`, `composite_observe`, SSE (composite), `cancel_composite_observe`, `remove_attributes` |
| `int_305` (`:615-621`) | int-305 Cancel Observe-Composite | `composite_observe` then `cancel_composite_observe` with the same 3 paths | logs `Observer removed for 1/0/1`, `3/0/11/0`, `3/0/16` | `composite_observe`, `cancel_composite_observe` |
| `int_306` (`:623-631`) | int-306 Send | Stream. shell `lwm2m send /1 /3`, log `SEND status: 0`, SEND event | not None; `verify_server_object(d[1])`; `verify_device_object(d[3])` | SSE SEND |
| `int_307` (`:633-640`) | int-307 Muting Send | write `1/0/23`=True (Mute Send). shell `send /3/0` output contains `can't do send operation`. write `1/0/23`=False, send, log `SEND status: 0` | as stated | `write`×2 (single bool) |
| `int_308` (`:643-683`, `@slow`) | int-308 Observe-Comp + Create | Shell deletes `/16/0`, `/16/1`; C.1. `create_obj_instance('16/0',{0:{0:'aa',1:'bb',2:'cc',3:'dd'}})`, log `Update Done`. write_attributes `16/0` {pmin:30,pmax:45}. `composite_observe(['/16/0','/16/1'])`. Stream (50 s): notif. `create_obj_instance('16/1',{0:{0:'11',1:'22',2:'33',3:'44'}})`, notif. Cancel, restore | create `CREATED(201)`; observe response `== {16:{0:{0:{0:'aa',1:'bb',2:'cc',3:'dd'}}}}` (exact; `/16/1` absent); first notif equal to that; 2nd create `CREATED(201)`; 2nd notif within `start+30-2 … start+45+2` and `== {16:{0:A,1:B}}` | `create_obj_instance`×2, `write_attributes`, `composite_observe`, SSE, `cancel_composite_observe`, `remove_attributes` |
| `int_309` (`:685-726`, `@slow`) | int-309 Observe-Comp + Delete | Same but creates both instances first; composite observe; notif; `delete('16/1')`; notif | creates `CREATED(201)`; observe response `== both`; notif `== both`; delete `['status']=='DELETED(202)'`; 2nd notif timing same window and `== {16:{0:A}}` | `create_obj_instance`×2, `write_attributes`, `composite_observe`, SSE, `delete`, `cancel_composite_observe`, `remove_attributes` |
| `int_310` (`:728-749`, `@slow`) | int-310 Observe-Comp + modify attrs | C.1. `composite_observe(['/1/0/1','/3/0'])`. Stream (50 s): write_attributes `3` {pmax:5}. Two notifications | write-attrs `CHANGED(204)`; notif `[3][0][0]=='Zephyr'`, `[1]=={0:{1:86400}}`, `start+5 > now-1`; second notif `start+5 > now-1` | `composite_observe`, `write_attributes`, SSE, `cancel_composite_observe`, `remove_attributes` |
| `int_311` (`:751-756`) | int-311 Send command | Stream (50 s). shell `lwm2m send /1/0/1 /3/0/11`, SEND event | `data == {3:{0:{11:{0:0}}}, 1:{0:{1:86400}}}` (exact) | SSE SEND |

### 2.4 `test_nosec.py` (fixture `endpoint_nosec`)
| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `test_LightweightM2M_1_1_int_101` (`:26-31`) | int-101 Initial Registration | none | `get('/clients/<ep>')` truthy | `get` |
| `…_int_105` (`:33-50`) Discarded Register Update | int-105 | Reads the registration and returns early if `secure`. CoAPthon sends `DELETE coap://192.0.2.2:5683/rd/<registrationId>` from the host (different source address), timeout 0.1, then `stop()`, sleep 1. Shell `lwm2m update` | `regid` truthy; DUT log `Failed with code 4\.4` (update got 4.04), then `Registration Done` within 10 s (re-register). **The server must accept an unsecured De-register from any peer when the registration has no security info** (Leshan `DefaultAuthorizer.checkIdentity`, `L/leshan-lwm2m-server/src/main/java/org/eclipse/leshan/server/security/DefaultAuthorizer.java:81-114`), and `registrationId` must be the `/rd/<id>` location segment | `get` + raw CoAP |

### 2.5 `test_observe_attributes.py` (fixture `endpoint`; `TEST_PMAX=4`, `TEST_TIME_ACC=0.5`, `:16-17`)
| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `test_attribute_gt_lt_st_wrong_res_type` (`:20-34`) | none | write_attributes gt/lt/st on `3/0/0` (string resource), then on `3/0/7` (integer). Removes gt, lt, st on `3/0/7` | the three on `3/0/0`: `['status'] != 'CHANGED(204)'` (HTTP must still be 2xx with JSON; the server must forward and not reject locally); the three on `3/0/7`: `== 'CHANGED(204)'` | `write_attributes`×6, `remove_attributes`×3 |
| `test_attribute_step` (`:37-67`, `@slow`) | none | `3/0/7` st=200, pmax=4. Observe. Stream 30 s. Writes 3000 → notif; 3100 → notif only at pmax (`start+4 ± 0.5`); 3500 → immediate | values 3000, 3100, 3500; timing as stated | `write_attributes`×2, `observe`, SSE, `cancel_observe`, `remove_attributes`×2 |
| `test_attribute_greater_than` (`:70-112`, `@slow`) | none | gt=4200, pmax=4. Writes 3000 (notif), 4100 (pmax), 4400 (immediate), 4300 (pmax), 4100 (notif) | values match; pmax ones within ±0.5 s of start+4 | same set |
| `test_attribute_less_than` (`:115-157`, `@slow`) | none | **Uses `gt`:3000, not `lt`** (`:120`), so it tests threshold crossing. Writes 3200, 3100 (pmax), 2800 (immediate), 2900 (pmax), 3100 | values match; pmax timing ±0.5 s | same set |

### 2.6 `test_portfolio.py` (fixtures `endpoint`, `configuration_C13`)
| Test | OMA ID | What it does | Exact assertions | REST |
|---|---|---|---|---|
| `…_int_1630` (`:27-51`) | int-1630 Create Portfolio | `discover('16/0')`. `create_obj_instance('16/1',{0:{0:'Host Device ID #2',1:'Host Device Model #2'}})`. `discover('16/1')`, `read('16')`. Shell delete `/16/1` | `/16/0`,`/16/0/0` present; `int(r['/16/0/0']['dim'])==4`; create `CREATED(201)`; `int(r['/16/1/0']['dim'])==2`; `read('16') == {16:{0:{0:{0:'Host Device ID #1',1:'Host Device Manufacturer #1',2:'Host Device Model #1',3:'Host Device Software Version #1'}},1:{0:{0:'Host Device ID #2',1:'Host Device Model #2'}}}}` (exact) | `discover`×2, `create_obj_instance`, `read` |
| `…_int_1635` (`:53-63`) | int-1635 Delete Portfolio | `configuration_C13` re-created at function scope (`@pytest.mark.parametrize(... indirect=...)`, `:54`). `discover('16')`, `delete('16/0')`, `discover('16')` | `/16/0`,`/16/0/0` present; delete `['status']=='DELETED(202)'`; final discover `== {'/16': {}}` (exact: one link, empty attribute dict) | `discover`×2, `delete` |

Count: blockwise 4, bootstrap 5, lwm2m 49, nosec 2, observe_attributes 4, portfolio 2, for **66** test functions. The `verify_*` helpers are not collected by pytest.

---

## 3. Leshan REST API surface used by `leshan.py`

### 3.1 Transport rules (client side, `I/pytest/leshan.py`)
- Base URL: `http://localhost:8080/api` (server) or `http://localhost:8081/api` (bootstrap) (`conftest.py:32-33`). One `requests.Session` per `Leshan` object (`leshan.py:28`).
- `handle_response` (`:36-55`): HTTP status outside `[200,300)` raises `RuntimeError`. A non-empty body is parsed with `json.loads`; an empty body returns `None`. Every LwM2M-level failure (401, 405, 404, …) must therefore be **HTTP 200 with a JSON response object**. The test then reads `['status']`.
- Constructor (`:22-34`): `GET {base}/security/clients?timeout=10&format=SENML_CBOR` must return a JSON **array**. A connection error raises `RuntimeError`, which makes the fixture skip. This is called on both servers.
- Request builders:
  - `get(path)` (`:57-63`): `GET base+path`, params `timeout=<self.timeout>` and `format=<self.format>` (omitted when format is `None`).
  - `put(path, data, uri_options)` (`:69-74`): `PUT base+path?timeout=<t>&format=<fmt><uri_options>`, header `content-type: application/json`, body is the JSON string. When format is `None` the query would be `format=None`; that case is never hit.
  - `put_raw(path, data, headers, params)` (`:65-68`): raw PUT with no default params.
  - `post(path, data)` (`:76-87`):
    - If `data` is not `None`: `POST base+path?timeout=<t>&format=<fmt>` with `content-type: application/json`.
    - If `data is None`: `POST base+path` with no params, no content-type and an empty body, so the server-side timeout defaults to 5 s.
  - `delete_raw(path)` (`:89-92`): `DELETE base+path`, no params.

### 3.2 Format names (`format`, `pathformat`, `nodeformat` query values)
Values used by the tests: `SENML_CBOR` (default), `SENML_JSON`, `TLV`, `JSON`, `TEXT`, `CBOR`, `OPAQUE`, and absent (`None`).
Leshan resolves them with `ContentFormat.fromName(param.toUpperCase())` and returns null for unknown names (`L/leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core/request/ContentFormat.java:47-64,154-161`). Known names: `TLV, JSON, TEXT, OPAQUE, LINK, SENML_JSON, SENML_CBOR, CBOR`.
Mapping to CoAP content formats: TEXT=0, LINK=40, OPAQUE=42, CBOR=60, SENML_JSON=110, SENML_CBOR=112, TLV=11542, JSON=11543.

### 3.3 Endpoint table — LwM2M server (`/api`, port 8080)
Servlet mounts: `/api/event/*` → EventServlet, `/api/clients/*` → ClientServlet, `/api/security/*` → SecurityServlet, `/api/server/*`, `/api/objectspecs/*` (`L/leshan-demo-server/src/main/java/org/eclipse/leshan/demo/server/LeshanServerDemo.java:307-336`).

`<ep>` = endpoint name. `<p>` = LwM2M path without a leading slash, as the harness writes it (e.g. `3/0/7`). The URL is `/clients/<ep>/<p>`.

| # | Harness method (leshan.py line) | HTTP | URL + query | Request body | Leshan handler | Response the harness parses |
|---|---|---|---|---|---|---|
| 1 | `__init__` (`:30`) | GET | `/security/clients?timeout=T&format=F` | none | `SS:109-128,170-179` | JSON array of SecurityInfo (§3.6); only `isinstance(list)` is checked |
| 2 | `get('/clients/<ep>')` (tests) | GET | `/clients/<ep>?timeout=T&format=F` | none | `CS:153-178,229-235` | Registration JSON (§3.5). Keys used: `lastUpdate`, `lifetime`, `queuemode`, `secure`, `registrationId`; truthiness. An unknown endpoint gives HTTP 400 text, which raises |
| 3 | `read(ep,p)` (`:253-267`) | GET | `/clients/<ep>/<p>?timeout=T[&format=F]` | none | `CS:192-214` (ReadRequest) | Response object (§3.4). If `success` is false the whole dict is returned (tests read `['status']`). Otherwise `content` is decoded by `kind`: `obj`, `instance`, `singleResource`/`resourceInstance` (bare value), `multiResource` (`{id:{riid:v}}`) |
| 4 | `write(ep,p,value)` (`:102-109`) | PUT | `/clients/<ep>/<p>?timeout=T&format=F` (no `replace`, so REPLACE) | `{"id":"<rid str>","kind":"singleResource"\|"resourceInstance","value":V,"type":"boolean\|integer\|time\|opaque\|string"}`. Kind is `singleResource` if the path has 3 segments, else `resourceInstance`. `id` is the **string** last path segment | `CS:307-369`, node parsed by `ND:54-144` | Response object; `['status']` checked |
| 5 | `update_obj_instance(ep,p,res)` (`:126-129`) | PUT | `/clients/<ep>/<p>?timeout=T&format=F&replace=false` | instance node (§3.7) | `CS:344-369` (Mode.UPDATE → CoAP POST) | Response object; `status=='CHANGED(204)'` |
| 6 | `replace_obj_instance` (`:131-134`) | PUT | `…&replace=true` | instance node | same, Mode.REPLACE → CoAP PUT | same |
| 7 | `create_obj_instance(ep,'<obj>/<inst>',res)` (`:136-140`) | POST | `/clients/<ep>/<obj>?timeout=T&format=F` (instance id stripped from the URL, kept in the body `id`) | instance node with `"id": <inst int>` | `CS:418-458` (2-3 path segments → create, `:455-458`), `CS:464-494` | Response object, `status=='CREATED(201)'`. Leshan adds `location` (`RS:58-59`), which the harness ignores |
| 8 | `execute(ep,p)` (`:98-100`) | POST | `/clients/<ep>/<obj>/<inst>/<res>` (no query, no body) | none | `CS:449-452` (exactly 4 segments incl. ep), `CS:496-510` | Response object; ignored by tests. HTTP must be 2xx |
| 9 | `delete(ep,p)` (`:94-96`) | DELETE | `/clients/<ep>/<p>` | none | `CS:560-603` | Response object, `status=='DELETED(202)'` |
| 10 | `write_attributes(ep,p,attrs)` (`:111-116`) | PUT | `/clients/<ep>/<p>/attributes?pmin=10&pmax=200…` (requests encodes the dict; ints → `"10"`) | none (no content-type) | `CS:336-338,371-388`. Attributes come from the raw query string via `DefaultLwM2mAttributeParser.parseUriQuery`. Path `0` gives a URL with 3 segments `[ep,"0","attributes"]`, and the lwm2m path becomes `/0/` (trailing slash) | Response object; `status=='CHANGED(204)'`, `'UNAUTHORIZED(401)'`, or `!= 'CHANGED(204)'`. An invalid attribute gives HTTP 400, which raises |
| 11 | `remove_attributes(ep,p,names)` (`:118-124`) | PUT | `/clients/<ep>/<p>/attributes?pmin&pmax` (valueless keys = unset) | none | same | ignored (HTTP must be 2xx) |
| 12 | `composite_read(ep,paths)` (`:320-325`) | GET | `/clients/<ep>/composite?pathformat=F&nodeformat=F&timeout=T&paths=/3,/1/0` (each path gets a leading `/`; comma-joined, URL-encoded by requests) | none | `CS:179-183,252-276` | Response object with `status=='CONTENT(205)'` and `content` = `{ "<path>": node, … }` (§3.4). Anything else raises in `parse_composite` (`:270-305`) |
| 13 | `composite_write(ep,res)` (`:327-363`) | PUT | `/clients/<ep>/composite?pathformat=F&nodeformat=F&timeout=T` | JSON object `{"/1/0/1": {"id":1,"kind":"singleResource","value":60,"type":"integer"}, "/16/0/0": {"id":0,"kind":"multiResource","values":{"0":"aa",…},"type":"string"}, "/x/y/z/i": {"id":i,"kind":"resourceInstance",…}}`. Here `id` is an **int**. Only `nodeformat` is used by Leshan | `CS:323-327,390-412` | Response object; `status=='CHANGED(204)'` |
| 14 | `discover(ep,p)` (`:365-370`) | GET | `/clients/<ep>/<p>/discover` (no query) | none | `CS:185-190,216-227` | `{"objectLinks":[{"url":"/3/0/7","attributes":{"dim":"2","pmin":"10",…}},…]}`, turned into `{url: attributes}`. Attribute values are core-link strings (`LK:41-55`); the harness applies `int()`/`float()`. Valueless attributes are not exercised. A discover response with status != success has no `objectLinks`, so a KeyError would be raised |
| 15 | `observe(ep,p)` (`:392-393`) | POST | `/clients/<ep>/<p>/observe?timeout=T&format=F` | `""` with `content-type: application/json` | `CS:439-444,512-529` | Response object (ObserveResponse ⊂ ReadResponse → has `content`). Return value ignored; HTTP must be 2xx |
| 16 | `cancel_observe(ep,p)` (`:395-396`) | DELETE | `/clients/<ep>/<p>/observe?active` | none | `CS:581-586,605-639`. With `active`: finds the SingleObservation for the path, sends a CoAP GET Observe=1 cancel, and returns its response object. **If there is no observation it returns HTTP 404, which raises** | ignored |
| 17 | `passive_cancel_observe(ep,p)` (`:398-399`) | DELETE | `/clients/<ep>/<p>/observe` | none | same, no `active` → local forget, HTTP 200 empty | none |
| 18 | `composite_observe(ep,paths)` (`:401-405`) | POST | `/clients/<ep>/composite/observe?pathformat=F&nodeformat=F&timeout=T&paths=…` (no body, no content-type) | none | `CS:433-437,531-557` | Same as #12 (`status` CONTENT(205) + `content` map) |
| 19 | `cancel_composite_observe(ep,paths)` (`:407-409`) | DELETE | `/clients/<ep>/composite/observe?paths=/1/0/1,/3/0/11/0,/3/0/16&active` (commas not encoded here; string concatenation) | none | `CS:575-579,641-679`. Matches the CompositeObservation whose path **list equals** the given list (order-sensitive), sends a CoAP cancel, otherwise HTTP 404 | ignored |
| 20 | `passive_cancel_composite_observe` (`:411-413`) | DELETE | same without `&active` | none | local forget | unused by tests |
| 21 | `create_psk_device(ep,passwd)` (`:372-374`) | PUT | `/security/clients/?timeout=T&format=F` (trailing slash) | `{"endpoint":"<ep>","tls":{"mode":"psk","details":{"identity":"<ep>","key":"<hex>"} } }` (hand-built string with extra spaces) | `SS:94-103,144-168`, `SD:37-82` | HTTP 200 empty. A non-unique identity gives 400, which raises |
| 22 | `delete_device(ep)` (`:376-377`) | DELETE | `/security/clients/<ep>` | none | `SS:133-142,195-206` | 200 with empty body, or `{"message":"not_found"}` (still 200) |
| 23 | `get_event_stream(ep,timeout)` (`:415-436`) | GET (stream) | `/event?<ep>`. **The query string is the bare endpoint name, not `ep=<name>`**, so Leshan's `req.getParameter("ep")` is null and the stream receives events for **all** endpoints (`ES:86,308-318,342-346`). Header `Accept: text/event-stream`; requests read timeout = `timeout` | none | `ES` (Jetty `EventSourceServlet`) | SSE, see §3.8 |

Server-side behaviours the harness depends on:
- **Queue mode** (`QH:100-147`): if the registration uses queue mode and the client is not awake, Leshan stores the request and ClientServlet answers HTTP 200 `{"ep":…,"path":…,"requestId":…,"delayed":true}` with no `status` (`CS:705-748`). Every test indexing `['status']` would then KeyError. The suite avoids this by running `lwm2m update` before each test (`conftest.py:44-49,180-184`) and relying on Leshan's 93 s awake window (`L/leshan-lwm2m-server/src/main/java/org/eclipse/leshan/server/queue/StaticClientAwakeTimeProvider.java:25-29`). **Recommendation for our server**: always send immediately; do not implement the delayed path.
- A request timeout makes `processDeviceResponse` return HTTP 504 `Request timeout` (`CS:750-762`), which raises in the harness. `test_blockwise_2` relies on this (or on the client-side timeout) to abort.
- Exceptions map to HTTP 400 (invalid request, codec, client sleeping) or 500 (rejected, cancelled, invalid response, other) as text bodies (`CS:278-301`). The harness raises on these.
- An unknown endpoint gives HTTP 400 `No registered client with id '<ep>'` (`CS:166-172`).

### 3.4 Response object JSON (`RS:45-69`)
Field order is a LinkedHashMap:
```json
{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,
 "content": <node> | {"<path>": <node>, ...},   // ReadResponse/ObserveResponse | ReadComposite/ObserveComposite
 "objectLinks":[{"url":"/3","attributes":{}}, ...],  // DiscoverResponse
 "location":"/16/1",                                // CreateResponse
 "errormessage":"..."}                              // only if failure && message non-empty
```
- `status` = `"<NAME>(<code>)"` (`L/leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core/ResponseCode.java:62-84,145-147`). Values asserted: `CONTENT(205)`, `CHANGED(204)`, `CREATED(201)`, `DELETED(202)`, `UNAUTHORIZED(401)`, `METHOD_NOT_ALLOWED(405)`. Other names: `BAD_REQUEST(400)`, `FORBIDDEN(403)`, `NOT_FOUND(404)`, `NOT_ACCEPTABLE(406)`, `REQUEST_ENTITY_INCOMPLETE(408)`, `PRECONDITION_FAILED(412)`, `REQUEST_ENTITY_TOO_LARGE(413)`, `UNSUPPORTED_CONTENT_FORMAT(415)`, `INTERNAL_SERVER_ERROR(500)`.
- `success` is used by `read()` (`leshan.py:256`).
- Composite `content` keys are `LwM2mPath.toString()`: `/` for root, otherwise `/o[/i[/r[/ri]]]` (`L/leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core/node/LwM2mPath.java:365-385`).
- Paths requested but absent in the device payload: Leshan's SenML decoder inserts `null` (`L/leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core/node/codec/senml/LwM2mNodeSenMLDecoder.java:133-141`). The demo ObjectMapper uses `setDefaultPropertyInclusion(NON_NULL)` (`CS:138`, `ES:297`), so null map values are dropped. The harness's `parse_composite` would crash on `null` (int-236, int-281, int-308, int-309). **Omit absent paths.**

### 3.5 Registration JSON (`RG:52-82`)
```json
{"endpoint":"client_a3","registrationId":"<id>","registrationDate":<epoch ms>,"lastUpdate":<epoch ms>,
 "address":"192.0.2.1:<port>","smsNumber":null-omitted,"lwM2mVersion":"1.1","lifetime":86400,
 "bindingMode":"U","rootPath":"/","objectLinks":[{"url":…,"attributes":{…}}],"secure":true,
 "additionalRegistrationAttributes":{},"queuemode":true,"availableInstances":{"1":[0],"3":[0]},
 "sleeping":false}
```
- Harness uses `lastUpdate` (compared to `time.time()*1000`, so it must be a JSON **number in ms**), `lifetime` (int), `queuemode` (bool), `secure` (bool, true for DTLS and false for nosec), and `registrationId` (string, equal to the `/rd/<id>` segment).
- `sleeping` is only present when queue mode is on (`RG:78-80`).

### 3.6 SecurityInfo JSON (`SD:37-157`, `L/leshan-demo-servers-shared/src/main/java/org/eclipse/leshan/demo/servers/json/JacksonSecuritySerializer.java:43-91`)
- PSK: `{"endpoint":"<ep>","tls":{"mode":"psk","details":{"identity":"<id>","key":"<hex>"}}}`.
- Other modes the parser accepts but tests do not use: `rpk` (`details.key` hex DER), `x509`, and `oscore` (`rid`, `sid`, `msec` hex).
- `GET /security/clients` returns an array of these objects.

### 3.7 LwM2mNode JSON
**Serializer (server → harness)** (`NS:55-123`):
| Node | JSON |
|---|---|
| Root | `{"kind":"root","objects":[<obj>,…]}` (no `id`) |
| Object | `{"id":3,"kind":"obj","instances":[<instance>,…]}` |
| Instance | `{"id":0,"kind":"instance","resources":[<resource>,…]}` |
| Single resource | `{"id":1,"kind":"singleResource","type":"INTEGER","value":"86400"}` |
| Multi resource | `{"id":7,"kind":"multiResource","type":"INTEGER","values":{"0":"3800","1":"5000"}}` |
| Resource instance | `{"id":0,"kind":"resourceInstance","type":"STRING","value":"test"}` |

- `id` is a JSON int. The harness uses it directly as a dict key, and tests index with ints.
- `type` is the enum name: `NONE, STRING, INTEGER, FLOAT, BOOLEAN, OPAQUE, TIME, OBJLNK, UNSIGNED_INTEGER, CORELINK` (`L/leshan-lwm2m-core/src/main/java/org/eclipse/leshan/core/model/ResourceModel.java:42-43`).
- Value encoding (`NS:104-123`): OPAQUE is a lowercase hex string; INTEGER, UNSIGNED_INTEGER and FLOAT are **strings**; CORELINK is a link-format string; STRING is a JSON string; BOOLEAN is a JSON bool; TIME is a `java.util.Date`, which Jackson writes as an epoch-ms number by default; OBJLNK is Jackson's default bean form.
- Harness decode (`leshan.py:208-217`):
  - `BOOLEAN` → `bool(value)`. The value **must be a JSON boolean**, because the string `"false"` would decode as True.
  - `INTEGER` → `int(value)`.
  - Everything else is returned raw, so `UNSIGNED_INTEGER` and `FLOAT` stay strings.
  - Every resource whose value is compared against a Python int (1/0/0-7, 3/0/6, 3/0/7, 3/0/8, 3/0/11, 5/…) must therefore be typed `INTEGER` (object model), not `UNSIGNED_INTEGER`.
- `multiResource.values` keys are strings, converted with `int()` (`leshan.py:228-229`).

**Deserializer (harness → server)** (`ND:54-262`):
- Kind detection order (`ND:76-138`): `kind=="obj"` or has `instances`; else `"instance"` or has `resources`; else `"multiResource"` or has `values`; else has `value`, giving `resourceInstance` if `kind=="resourceInstance"` and a single resource otherwise.
- `id` is read with `asInt()`, so both `"1"` and `1` are accepted. An instance without `id` gets an undefined id, which turns a create into "server picks id" (`CS:478-483`).
- `type` is case-insensitive (`Type.valueOf(upper)`). The harness sends lowercase `boolean|integer|time|opaque|string` (`leshan.py:143-160`).
- Values (`ND:146-262`):
  - BOOLEAN must be a JSON bool.
  - STRING must be text.
  - INTEGER accepts a string or an integral number (the harness sends numbers).
  - TIME accepts an integral number and makes `new Date(n)` (**milliseconds**). The harness sends `int(datetime.timestamp())` **seconds** (`leshan.py:166-167`); only int-257 uses this, with value 0.
  - OPAQUE is a hex string; the harness hexes bytes (`:168-169`).
  - FLOAT accepts a string or number; OBJLNK is `{objectId,objectInstanceId}`; CORELINK is text; UNSIGNED_INTEGER accepts a string or number.
- Harness instance body (`leshan.py:173-206`): `{"kind":"instance","id":<int>,"resources":[{"id":<int>,"kind":"singleResource","value":…,"type":…}, {"id":<int>,"kind":"multiResource","values":{<riid>: v},"type":<type of first value>}]}`. When `json.dumps` serializes `values`, int keys become strings.
- Text bodies: ClientServlet also accepts `text/plain` as a string single resource (`CS:779-783`). The harness never uses it.

### 3.8 Event stream (SSE)
- Wire format (Jetty `EventSource.Emitter.event(name, data)`): `event: <NAME>\n` then `data: <single-line JSON>\n` then a blank line. The harness matches the exact line `event: <NAME>`, then reads lines until the first one starting with `data: ` (`leshan.py:445-464`).
  - Keep JSON on one line. Leshan's event mapper does not indent (`ES:296-305`).
  - Iteration uses `iter_lines(chunk_size=1)`. The harness overall timeout is only checked when a line arrives (`:465-466`). A silent stream makes `requests` raise, which surfaces from `iter_lines` as `ConnectionError`, not `Timeout`, and is **not caught**.
  - Jetty's `EventSourceServlet` sends a heartbeat (`\r\n`) every 10 s by default (Jetty behaviour, not in this clone). **Recommendation**: send a blank-line or comment heartbeat well under the smallest stream timeout (10 s), e.g. every 2-5 s.
- Events emitted by Leshan (`ES:77-84`): `REGISTRATION`, `UPDATED`, `DEREGISTRATION`, `SLEEPING`, `AWAKE`, `NOTIFICATION`, `SEND`, `COAPLOG` (only when an `ep` filter is present, `ES:362-364`), and `REQUEST_RESPONSE` (delayed queue-mode responses, `CS:718`). The harness consumes only `NOTIFICATION` and `SEND`.
- `NOTIFICATION`, single observation (`ES:159-184`): `{"ep":"<ep>","kind":"single","res":"/3/0/7","val":<node>}`. The harness builds `{res: val}` and runs `parse_composite` on it (`leshan.py:461-463`), giving e.g. `{3:{0:{7:{0:3000}}}}`.
- `NOTIFICATION`, composite (`ES:186-219`): `{"ep":"<ep>","kind":"composite","val":{"<path>":<node>,…},"paths":["/1/0/1",…]}`. The harness runs `parse_composite(val)` (`leshan.py:459-460`). Absent paths must be omitted.
- `SEND` (`ES:245-272`): `{"ep":"<ep>","val":{"<path>":<node>,…}}`, built from `data.getMostRecentNodes()`. When Leshan decodes SenML without request paths, the keys are per record, i.e. resource (`/3/0/0`) or resource-instance (`/3/0/11/0`, `/19/0/0/0`) paths (`LwM2mNodeSenMLDecoder.java:150-163`). `parse_composite` merges both depths, so object-level keys are also fine.
- Filtering: `sendEvent` delivers to sources whose endpoint is null or equal (`ES:308-318`). The harness's malformed query means it always gets the unfiltered stream.
- `parse_composite` (`leshan.py:270-305`):
  - If the payload has `status`, it must equal `CONTENT(205)` and have `content`.
  - Key `/` expects `{"objects":[obj…]}`.
  - 1-segment key → obj node; 2 → instance node; 3 → resource node (single or multi); 4 → resourceInstance node.
  - Deeper keys raise.

---

## 4. Bootstrap server and bootstrap REST API

### 4.1 Tests that use bootstrap
- All PSK modules (blockwise, bootstrap, lwm2m, observe_attributes, portfolio) go through `endpoint_bootstrap`, so every PSK registration starts with a client-initiated bootstrap over DTLS-PSK to `192.0.2.2:5784`.
- Bootstrap-specific tests: int-1 (+0, +101, +401), int-4, int-5, int-6, int-7 (§2.2).
- Bootstrap-Request trigger: `execute('1/0/9')` on the **LwM2M server** API (int-4, int-5). The DUT then re-bootstraps against the BS server and re-registers.
- The CoAP BS sequence is not observed directly. The tests only see DUT logs and resulting state. What Leshan's BS server does with the config below:
  1. Delete `/0` and `/1` (Zephyr keeps the BS account).
  2. Write `/1/0`.
  3. Write `/0/1`.
  4. Bootstrap-Finish.

  int-4 checks that the locally created `/1/2` is gone afterwards, which proves `toDelete:["/1"]` is applied.

### 4.2 Bootstrap REST endpoints used (`/api`, port 8081)
Mounts (`L/leshan-demo-bsserver/src/main/java/org/eclipse/leshan/demo/bsserver/LeshanBootstrapServerDemo.java:234-258`): `/api/bootstrap/*` → BootstrapServlet, `/api/server/*`, `/api/security/*` → SecurityServlet (same class as §3.6), `/api/event/*`.

| Harness (leshan.py) | HTTP | URL | Body | Handler | Response |
|---|---|---|---|---|---|
| `__init__` (`:30`) | GET | `/security/clients?timeout=T&format=F` | none | `SS:109-128` | JSON array |
| `create_bs_device` part 1 (`:379-381`) | PUT | `/security/clients/?timeout=T&format=F` | `{"tls":{"mode":"psk","details":{"identity":"<ep>","key":"<hex(bs_passwd)>"}},"endpoint":"<ep>"}` | `SS:94-103,144-168` | 200 empty |
| `create_bs_device` part 2 (`:382-386`) | POST | `/bootstrap/<ep>?timeout=T&format=F` (content-type json) | BootstrapConfig (below) | `BS:81-102,136-153` | 200 empty. Invalid → 400 text, which raises |
| `delete_bs_device` (`:388-390`) | DELETE | `/security/clients/<ep>` | none | `SS:133-142,195-206` | 200 (empty or `{"message":"not_found"}`) |
| `delete_bs_device` | DELETE | `/bootstrap/<ep>` | none | `BS:104-126,155-163` | **204 No Content**; if missing → **404**, which makes the fixture teardown raise |

Not used by the harness: `GET /bootstrap` (all configs, `BS:71-79,128-134`).

### 4.3 BootstrapConfig JSON body exactly as sent (`leshan.py:384`)
```json
{"servers":{"0":{"binding":"U","defaultMinPeriod":1,"lifetime":86400,"notifIfDisabled":false,"shortId":1}},
 "security":{"1":{"bootstrapServer":false,"clientOldOffTime":1,
                  "publicKeyOrId":[99,108,105,...],        // ASCII codes of endpoint name (PSK identity)
                  "secretKey":[97,98,...],                  // ASCII codes of server PSK
                  "securityMode":"PSK","serverId":1,"serverSmsNumber":"",
                  "smsBindingKeyParam":[],"smsBindingKeySecret":[],"smsSecurityMode":"NO_SEC",
                  "uri":"coaps://192.0.2.2:5684"}},
 "oscore":{},"toDelete":["/0","/1"]}
```
- Byte arrays are JSON arrays of ints 0-255 (Python `str([ord(c)…])`). Jackson's default `byte[]` deserializer accepts them. The serializer emits unsigned int arrays (`BS:67`, `L/leshan-demo-bsserver/src/main/java/org/eclipse/leshan/demo/bsserver/json/ByteArraySerializer.java:43-52`).
- `binding` is a string parsed by `BindingMode.parse` (`L/leshan-demo-bsserver/src/main/java/org/eclipse/leshan/demo/bsserver/json/EnumSetBindingModeDeserializer.java:29-40`, selected in `EnumSetDeserializer.java:43-49`).
- Map keys are instance ids: `servers."0"` → `/1/0`, `security."1"` → `/0/1`.
- Field defaults and the full schema (`BC:51-525`):
  - top level: `autoIdForSecurityObject=false` (`:65`), `contentFormat=null` (`:72`), `toDelete` (`:77`), `servers` (`:82`), `security` (`:87`), `acls` (`:92`), `oscore` (`:97`).
  - ServerConfig (`:100-225`): `shortId`, `lifetime=86400`, `defaultMinPeriod=1`, `defaultMaxPeriod`, `disableTimeout`, `notifIfDisabled=true`, `binding={U}`, `apnLink`, `registrationPriority`, `initialDelay`, `registrationFailure`, `bootstrapOnRegistrationFailure`, `communicationRetryCount`, `CommunicationRetryTimer`, `SequenceDelayTimer`, `SequenceRetryCount`, `trigger`, `preferredTransport`, `muteSend`.
  - ServerSecurity (`:246-397`): `uri`, `bootstrapServer=false`, `securityMode` (enum name), `publicKeyOrId`, `serverPublicKey`, `secretKey`, `smsSecurityMode=NO_SEC`, `smsBindingKeyParam`, `smsBindingKeySecret`, `serverSmsNumber=""`, `serverId`, `clientOldOffTime=1`, `bootstrapServerAccountTimeout=0`, `oscoreSecurityMode`, `matchingType`, `sni`, `certificateUsage`, `cipherSuite`.
  - ACLConfig (`:416-448`); OscoreObject (`:470-501`).
- Minimal subset to implement: the fields above that the harness sends. Unknown fields should be ignored. Leshan's `FAIL_ON_UNKNOWN_PROPERTIES` is Jackson's default (true), but the harness sends none.

### 4.4 Expected end state on the DUT after bootstrap
- `/0/0` BS account: `coaps://192.0.2.2:5784`, PSK, identity `<ep>`, key `<bs_passwd>`. This is kept.
- `/0/1`: `coaps://192.0.2.2:5684`, mode 0 (PSK), identity `<ep>`, key `<passwd>`, SSID 1.
- `/1/0`: SSID 1, lifetime 86400, pmin 1, pmax default (the tests expect `/1/0/3 == 10`, the DUT default), `notifIfDisabled` false, binding `U`.
- Tests assume `/1/0/5` (disable timeout) is 86400 and `/1/0/6` is False (`test_lwm2m.py:108-116`).

---

## 5. Skipped / xfail / conditional tests
- **No** `pytest.mark.skip`, `skipif` or `xfail` anywhere in the suite.
- Runtime skips:
  - `leshan` fixture (`conftest.py:62-66`) and `leshan_bootstrap` fixture (`:76-79`): skip if the REST API is unreachable or `/security/clients` is not a list.
  - `helperclient` (`:89-92`): skips if CoAPthon3 is missing, so only int-105 is affected.
- Soft skip: int-105 returns early (passes) if the registration is `secure` (`test_nosec.py:39-41`). It is always non-secure under `endpoint_nosec`.
- `@pytest.mark.slow` (still run by twister because of `slow: true` / `--enable-slow`): int-7 (`test_bootstrap.py:80`), int-107 (`test_lwm2m.py:65`), int-109 (`:81`), int-301 (`:519`), int-304 (`:583`), int-308 (`:643`), int-309 (`:685`), int-310 (`:728`), `test_attribute_step` (`test_observe_attributes.py:37`), `test_attribute_greater_than` (`:70`), `test_attribute_less_than` (`:115`).
- Not implemented, per README status (`README.md:124-194`):
  - int-2 (Bootstrap Cert, "testcase not implemented").
  - int-3 (Smartcard, no support).
  - int-8 (Bootstrap Read, "cannot be implemented from client side").
  - int-9 (Bootstrap and Configuration Consistency, "not implemented").
- README lists int-0 and int-101 as passing. They are covered by the `verify_*` helpers and `test_nosec.py::int_101`. Rows 222-226 appear twice in the README table (`:153-162`).
- Fixed upstream issues the README references (struck through, so resolved): #64011 (int-228), #64012 and #64189 (int-229), #64290 (int-306), #64634 (int-308, int-309).
- Known test quirks:
  - `test_attribute_less_than` sets `gt`, not `lt` (`test_observe_attributes.py:120`).
  - `test_blockwise_3` restores `leshan.format` from the loop variable (`test_blockwise.py:99-105`).
  - int-261 notes Zephyr does not support epmin/epmax but still expects `CHANGED(204)` (`test_lwm2m.py:480,486`).
  - TIME values are sent in seconds but Leshan reads ms (`leshan.py:166-167` vs `ND:200-203`).

---

## 6. Implementation checklist for a drop-in replacement (derived from the above)
1. Two HTTP listeners: 8080 (server) and 8081 (bootstrap), both under `/api`. CoAP 5683, CoAPS 5684 (PSK, CID), BS CoAPS 5784.
2. `/api/security/clients` GET/PUT (trailing slash allowed)/DELETE on both listeners. The PSK store is keyed by endpoint and identity. Reject NoSec registrations for endpoints that have security info.
3. `/api/bootstrap/<ep>` POST/DELETE (204/404). Run the BS sequence: delete `toDelete` paths, write `/1/<i>` and `/0/<i>` from the config, then Bootstrap-Finish.
4. `/api/clients/<ep>` GET (registration JSON), and the per-path ops in §3.3 #3-#20 with exact routing rules:
   - POST with 4 segments → execute; POST with 2-3 segments → create.
   - `/attributes` suffix → write-attributes, built from the raw query (valueless key = unset).
   - `/discover` suffix → discover.
   - `/observe` suffix → observe or cancel (`?active` → CoAP cancel; else local forget, then RST on the next notify).
   - `composite` → composite ops.
5. All device outcomes are returned as HTTP 200 + response object JSON with `status` `"NAME(code)"`. Never queue for sleeping clients.
6. Node JSON per §3.7 (int ids, INTEGER as strings, JSON booleans, hex opaque). Omit absent composite paths.
7. SSE `/api/event` that ignores a malformed query, emits `NOTIFICATION`/`SEND` in the §3.8 shapes, and sends a frequent heartbeat.
8. Block1 (write ≥5000 B opaque, abortable on timeout) and Block2 (read 4 KiB TLV/SenML-CBOR) on the downlink side. Block1 on uplink Send (`/dp`, 4 KiB SenML-CBOR).
9. Accept an unsecured De-register (`DELETE /rd/<id>`) from any peer when no security info exists. Then respond 4.04 to the stale Update.
10. Object models (types must match §3.7) for 0, 1, 3, 5, 16, 19 at least. Tolerate unknown objects 3339/3353 in composite paths.
