# Zephyr LwM2M client: wire profile

Source: Zephyr `74b7173e9c929cf8eed570fb3df099c5514720c0`. This profile describes what the client actually sends and accepts, based on reading the code. Where a choice depends on Kconfig, the option is named and its default given.

**Citation aliases.** The following are relative to `subsys/net/lib/lwm2m/`:
`rd` = `lwm2m_rd_client.c`, `mh` = `lwm2m_message_handling.c`, `eng` = `lwm2m_engine.c`, `obs` = `lwm2m_observation.c`, `reg` = `lwm2m_registry.c`, `lf` = `lwm2m_rw_link_format.c`, `srv` = `lwm2m_obj_server.c`, `sec` = `lwm2m_obj_security.c`, `fw` = `lwm2m_obj_firmware.c`, `pull` = `lwm2m_pull_context.c`, `K` = `Kconfig`, `tlv` = `lwm2m_rw_oma_tlv.c`, `sj` = `lwm2m_rw_senml_json.c`, `sc` = `lwm2m_rw_senml_cbor.c`.
Headers: `lwm2m.h` = `include/zephyr/net/lwm2m.h`, `socket.h` = `include/zephyr/net/socket.h`, `coap.h` = `include/zephyr/net/coap.h`.
`subsys/net/lib/coap` is **not** in the sparse clone. CoAP-layer defaults marked † are upstream Zephyr defaults. They were not verified in this tree.

---

## 1. Protocol version and Register contents

### 1.1 Version selection

| Item | Behaviour | Cite |
|---|---|---|
| Versions | Only **1.0** (`LWM2M_VERSION_1_0`, the default) and **1.1** (`LWM2M_VERSION_1_1`) exist. **1.2 is not implemented.** The version is fixed at build time and the client does not negotiate it. | K:21-36 |
| `lwm2m=` value | `"1.0"` or `"1.1"` (`LWM2M_PROTOCOL_VERSION_STRING`) | lwm2m_engine.h:17-26 |
| 1.0 implies | `LWM2M_RW_OMA_TLV_SUPPORT` | K:29 |
| 1.1 implies | `LWM2M_RW_CBOR_SUPPORT`, `ZCBOR`. SenML is **not** implied and must be enabled explicitly. | K:33-34 |
| Security/Server obj ver | Follows the protocol version by default: 1.0 gives 1.0, 1.1 gives 1.1 | K:38-66 |
| Send, `/dp`, resource-instance paths, `Q` query parameter | 1.1 only | mh:3570,3708-3711; rd:989-1001; mh:1540 |

### 1.2 Register (`POST /rd`)

| Field | Value | Cite |
|---|---|---|
| Type, code, token | CON, POST, new 8-byte random token | rd:905-908; mh:623-625; coap.h:629-634 |
| Uri-Path | `rd` | rd:917-919 |
| Content-Format | `40` (application/link-format) | rd:933-938 |
| Uri-Query order | `lwm2m=<ver>`, `ep=<name>`, `lt=<sec>`, then optionally `b=<binding>` and, in 1.1 only, `Q` | rd:942-1002 |
| `lt` | Always sent on Register. Value is `/1/x/1`. If that is unset, or lower than `LWM2M_ENGINE_DEFAULT_LIFETIME` (default **30 s**, minimum 15), the client raises it to the default and also writes the raised value back to `/1/x/1`. | rd:671-692,962-973; K:526-533 |
| `b` | Omitted when binding is `U` and queue mode is off. 1.0 sends `b=UQ` in queue mode. 1.1 sends `b=U` and a bare `Q` query option. Only UDP is supported. | rd:975-1002; reg:1131-1151 |
| `sms`, `sn`, `apn`, `Q`-less 1.1 queue | **Never sent**. There is no SMS support. | rd:890-1034 |
| `ep` length | Up to `LWM2M_RD_CLIENT_ENDPOINT_NAME_MAX_LENGTH - 1` = **32 chars** (default 33) | K:351-355; rd:1616-1617 |
| Payload | CoRE link format (see 1.3) | rd:1004-1017 |
| Block1 | Used only if `LWM2M_COAP_BLOCK_TRANSFER=y` (default **n**) and the payload is larger than `LWM2M_COAP_MAX_MSG_SIZE`. Otherwise an oversized payload fails to build. | mh:388-438,712-725 |
| Success | **2.01 Created** with at least 2 Location-Path options. The client stores only `options[1]` (max 32 bytes) as its registration ID and uses `/rd/<id>` for every later request. Anything else counts as failure. | rd:530-562 |

### 1.3 Registration payload (link format)

| Rule | Detail | Cite |
|---|---|---|
| Prefix | `</>;ct=112` if SenML-CBOR is enabled, else `</>;ct=110` if SenML-JSON is enabled, else `</>;ct=11543` if OMA-JSON is enabled, else no prefix. Raw CBOR (60) and TLV never appear in `ct`. | lf:34-42,67-75 |
| `rt="oma.lwm2m"` | **Never sent.** There is no alternate-path support (TODO). | lf:24-27 |
| Security object `/0` | Excluded | mh:845-848 |
| Object with 0 instances | `</X>` alone | mh:850-861 |
| Object with instances | `</X>;ver=M.m` (only when the version must be reported), followed by `</X/i>` for each instance. No resources and no attributes. | mh:853-873; lf:320-353,392-394 |
| `ver=` rule | Always added if `LWM2M_ENGINE_ALWAYS_REPORT_OBJ_VERSION=y` (default n). For non-core objects, added when the version is not 1.0. For core objects, added when the version differs from the table in reg:62-86. | reg:1451-1473 |
| Quirk | Device object `/3` is hard-coded as **1.0**, while the 1.1 core table expects 1.1. A 1.1 client therefore reports `</3>;ver=1.0`. | lwm2m_obj_device.c:28-29; reg:76 |
| Separator | `,` with no whitespace. `ver` is formatted with `;ver=%u.%u`. | lf:88-119 |
| Example (1.1, SenML-CBOR) | `</>;ct=112,</1>;ver=1.1,</1/0>,</3>;ver=1.0,</3/0>,</5/0>,</3303/0>` | derived |

---

## 2. Registration lifecycle

### 2.1 State machine

States are in rd:94-114 and dispatch is in rd:1453-1577. A guard timeout `EXCHANGE_LIFETIME` of 247 s applies in every `*_SENT` state and in `BOOTSTRAP_REG_DONE`. When it expires the client goes to `INIT` (rd:1564-1573).

| From | Event | To / action | Cite |
|---|---|---|---|
| IDLE | `lwm2m_rd_client_start()` | INIT | rd:1579-1625 |
| INIT | always | Stop engine, reset lifetime and last_update. Go to `DO_BOOTSTRAP_REG` if bootstrap is flagged, else `DO_REGISTRATION`. | rd:734-749 |
| DO_REGISTRATION | not suspended | Pick a server (see 2.4), find its Security instance by SSID, parse the URI, open the socket, connect, and send Register. The client **always calls `lwm2m_engine_context_close`**, which drops all observations and pending messages. If a socket already exists and `close_socket` is not set, it is reused. | rd:1062-1143; mh:442-475 |
| REGISTRATION_SENT | 2.01 | REGISTRATION_DONE. Retries are reset and queued messages are flushed. | rd:530-562,223-225 |
| REGISTRATION_SENT | 4.xx/5.xx, or bad Location-Path | The server is disabled for `MAX_RETRIES × 247 s` (1235 s by default). Emits `REGISTRATION_FAILURE`, stops the engine, and goes to NETWORK_ERROR. | rd:565-573,76,355-382 |
| REGISTRATION_SENT | CoAP timeout | NETWORK_ERROR | rd:575-580 |
| REGISTRATION_DONE | `now ≥ next_update()` or trigger | UPDATE_REGISTRATION after a 100 ms delay | rd:1183-1202 |
| REGISTRATION_DONE | queue mode and idle ≥ `QUEUE_MODE_UPTIME` | REGISTRATION_DONE_RX_OFF (socket action per 6.5) | rd:1193-1197,252-259 |
| UPDATE_REGISTRATION | | Resume the connection and send Update. If sending fails, stop the engine and go to DO_REGISTRATION. | rd:1204-1246 |
| UPDATE_SENT | 2.04 **or 2.01** | REGISTRATION_DONE. Queued Notify and Send messages are flushed. | rd:582-603,218-222 |
| UPDATE_SENT | **any other code (4.04, 4.00, …)** | `context_close` (observations dropped), then SEND_REGISTRATION: an immediate full Register **on the same socket and DTLS session** | rd:605-611,367-370,1036-1043 |
| UPDATE_SENT | CoAP timeout | `close_socket=true`, then DO_REGISTRATION. The socket is closed and a new DTLS handshake precedes the Register. | rd:614-623,1083-1088 |
| any registered | Notify or Send CON timeout | `lwm2m_rd_client_timeout`, then DO_REGISTRATION | mh:2960-2982,3523-3530; rd:1811-1826 |
| DEREGISTER | | `DELETE /rd/<id>` (CON, 8-byte token). Expects 2.02. Any response or a timeout goes to DEREGISTERED. | rd:1248-1313,625-656 |
| DEREGISTERED | | Stop the engine, then IDLE, or SERVER_DISABLED if `/1/x/4` was executed | rd:1546-1553 |
| SERVER_DISABLED | | Check every 60 s whether any server is enabled again, and go to INIT once one is | rd:1524-1535 |
| socket error (poll ERR/HUP, recv/send errno) | | Close the socket, then NETWORK_ERROR, or IDLE if not in a running state | rd:385-417; eng:898-903,917-926 |

### 2.2 Update (`POST /rd/<id>`)

| Item | Behaviour | Cite |
|---|---|---|
| Path, type, token | Uri-Path `rd`, `<id>`. CON, new 8-byte token. | rd:917-931,905-908 |
| Query | `lt=<n>` **only if the lifetime changed**. No `lwm2m`, `ep`, `b` or `Q`. | rd:942,962-973,978 |
| Payload | Object links with CF 40 **only** when `update_objects` is set (an object instance was created or deleted locally or by the server). Otherwise the Update is empty. | rd:933-940,1004-1017,1209-1220; reg:334,353,1410; mh:1631-1633 |
| Schedule | `next = max(min(UPDATE_PERIOD or lifetime, lifetime − SECONDS_TO_UPDATE_EARLY), 15 s)` measured from the last successful Register/Update. With the defaults (lt 30, early 10, period 0) this gives **an Update every 20 s**. | rd:1145-1162; K:535-553 |
| Triggers | Execute `/1/x/8`, write `/1/x/1` (lifetime), local or remote create/delete of an instance (with payload), `lwm2m_rd_client_update()`, waking from queue RX-off (see 6.5) | srv:120-125,146-161; rd:420-437,1763-1766,1784-1806 |
| Trigger delay | 100 ms (`DELAY_FOR_ACK`) so that the ACK to the triggering request goes out first | rd:73,430-431 |
| Network error while registered | If `last_update` is set and the lifetime has not expired, the client **reopens the socket (new DTLS handshake, possibly a new source port or IP) and sends an Update, not a Register**. The server must accept an Update for `<id>` from a new transport address or session. | rd:1414-1435 |

### 2.3 Retries and backoff (`sm_do_network_error`, rd:1332-1451)

- Each entry closes the socket. Delays between attempts are 0 s, 1 s, 2 s, 4 s, 8 s and 16 s (`retry_delay = 1 << retries`, applied on the next entry). See rd:1340-1346.
- When `retries > LWM2M_RD_CLIENT_MAX_RETRIES` (default 5), the client disables the current server for 1235 s. It then tries, in order: another bootstrap server if it was bootstrapping; another server if that server has lower priority or the client was registered before; `SERVER_DISABLED` if a server was disabled; fallback to bootstrap (only if `RD_CLIENT_SUPPORT_BOOTSTRAP` is set and `/1/x/16` is true, default `LWM2M_SERVER_BOOTSTRAP_ON_FAIL=y`). If none applies it stops, emits `NETWORK_ERROR` and goes to **IDLE**, and the application must restart it. See rd:1349-1399,1315-1330,1438-1450; K:555-559.
- Retries reset to 0 on a successful Register or Update (rd:553,599).

### 2.4 Server and Security object handling

| Item | Behaviour | Cite |
|---|---|---|
| Server selection | Lowest `/1/x/13` priority (1.1 only; otherwise all equal). Disabled servers are skipped, as is SSID 0 or 65535. Instance order is used within a priority. | srv:263-309 |
| Security link | Server `/1/x/0` SSID is matched to `/0/y/10` | rd:1105-1118 |
| URI | `/0/y/0`. Only `coap://` and `coaps://` are accepted. Default ports are `LWM2M_PEER_PORT` (5683) and, for firmware, 5683/5684. IP literal is tried first (v6, then v4), then DNS (`LWM2M_DNS_SUPPORT`). | mh:3330-3461; K:190-206 |
| Lifetime | `/1/x/1`. Values below the default are clamped up. Writing it triggers an Update. | rd:671-692; srv:146-161 |
| Execute `/1/x/4` Disable | After 1 s, Deregister. The server stays disabled for `/1/x/5` (default 86400 s). | srv:97-118,357; rd:1740-1761 |
| Execute `/1/x/9` (1.1) | Server-initiated bootstrap after 1 s. Returns 4.05 if already bootstrapping. | srv:127-131; rd:493-515 |
| `/1/x/23` Mute Send | Send returns `-EPERM` locally | mh:3591-3594 |
| Default instances | Security `/0/0` is always created. Server `/1/0` is auto-created only when bootstrap support is disabled. | sec:257-261; srv:449-457 |
| Instance limits | Security and Server: 1 instance, or 2 with bootstrap support. Range 1-10. | K:374-390 |

### 2.5 Bootstrap (`LWM2M_RD_CLIENT_SUPPORT_BOOTSTRAP`, default n)

| Step | Wire behaviour | Cite |
|---|---|---|
| Choose BS server | The next Security instance with `/0/x/1 = true` | rd:703-730 |
| Bootstrap-Request | CON `POST /bs?ep=<name>` with an 8-byte token and no payload. **1.1 only** adds `pct=<112 \| 110 \| 11542>`: SenML-CBOR if enabled, else SenML-JSON, else TLV. | rd:752-820 |
| Response | **2.04** goes to BOOTSTRAP_REG_DONE. Any other code is **not retried**: the client emits `BOOTSTRAP_REG_FAILURE` and goes to IDLE. A timeout goes to NETWORK_ERROR and retries. | rd:460-490 |
| Window | The entire bootstrap must finish within 247 s of entering REG_DONE, otherwise the client restarts at INIT | rd:1487-1490,1564-1573 |
| Server ops | Write (PUT), Read, Discover, Delete and Finish. While in bootstrap mode, paths with no Uri-Path (`/`) are allowed for GET and DELETE. Writes to RO resources of `/0` and `/1` are allowed. Missing instances are auto-created. See section 3. | mh:2231-2251; eng:255-268 |
| Bootstrap-Finish | `POST /bs` always returns **2.04**. There is no consistency check and no 4.06. The client then waits 1 s, stops the engine, resets the security instance, and Registers. A socket error right after the ACK is ignored. | mh:2370-2385; rd:859-887,395-400 |
| `lwm2m=` in BS-Discover | 1.1: `</>;lwm2m=1.1`. 1.0: `</>;lwm2m="1.0"` (quoted). | lf:18-22 |

---

## 3. Inbound operations (server to client)

### 3.1 Dispatch (`handle_request`, mh:2311-2642)

| CoAP | Condition | Operation | Success code | Cite |
|---|---|---|---|---|
| GET | `Accept: 40` | Discover (Bootstrap-Discover in BS mode) | 2.05 | mh:2436-2447 |
| GET | otherwise | Read. With `Observe: 0` it is Observe, with `Observe: 1` it is Cancel. | 2.05 | mh:2441-2451,2526-2559 |
| FETCH | | Read-Composite, or Observe-Composite / Cancel with `Observe` | 2.05 | mh:2453-2458 |
| iPATCH | | Write-Composite | 2.04 | mh:2460-2463 |
| POST | path level 1 | Create | **2.01** | mh:2466-2469 |
| POST | path level 2 | Write (partial update) | 2.04 | mh:2470-2473 |
| POST | path level 3 or 4 | Execute | 2.04 | mh:2474-2477 |
| POST | `/bs` (BS support) | Bootstrap-Finish | 2.04 | mh:2370-2385 |
| PUT | no Content-Format | Write-Attributes | 2.04 | mh:2481-2490 |
| PUT | with Content-Format | Write (**same code path as POST; replace semantics are not implemented**) | 2.04 | mh:2481-2490,2565-2573 |
| DELETE | | Delete (Bootstrap-Delete in BS mode) | 2.02 | mh:2492-2495,2590-2598 |
| NON request | | **Ignored** with no response (only CON requests are processed) | | mh:2899-2952 |

Responses are piggy-backed ACKs that echo the request token. If the application calls `lwm2m_acknowledge()`, the client sends an empty ACK first, then a separate **CON** response with a new MID (mh:810-829,2644-2688,2934-2941).

### 3.2 Error mapping (mh:2611-2634)

| errno | Code |
|---|---|
| ENOENT | 4.04 Not Found |
| EPERM | 4.05 Method Not Allowed |
| EEXIST | 4.00 Bad Request |
| EFAULT | 4.08 Request Entity Incomplete |
| EFBIG | 4.13 Request Entity Too Large |
| ENOTSUP | 5.01 Not Implemented |
| ENOMSG | 4.15 Unsupported Content-Format |
| EACCES | **4.01 Unauthorized** |
| ECANCELED | 4.06 Not Acceptable |
| anything else (ENOMEM, EINVAL, EOPNOTSUPP, ESRCH, …) | 5.00 Internal Server Error |

### 3.3 Common pre-checks

| Check | Result | Cite |
|---|---|---|
| Unknown Content-Format | 4.15 | mh:2394-2401,936-986 |
| Unknown Accept | 4.06 | mh:2404-2418,879-934 |
| No Accept | 1.1: SenML-CBOR, else SenML-JSON, else CBOR(60), else TLV. 1.0: TLV. If none of these is compiled in: 5.01. | mh:2253-2282 |
| Empty Uri-Path outside BS, FETCH or iPATCH | 4.05 | mh:2357-2368 |
| Non-numeric path segment | 4.04 | mh:2387-2391,490-507 |
| Object not registered | 4.04 | mh:2421-2431 |
| `/0/...` outside BS mode | **4.01** | mh:2518-2522 |
| Access Control (`LWM2M_ACCESS_CONTROL_ENABLE`, default n) | Per-server ACL check | mh:2507-2517 |
| Gateway prefix (`LWM2M_GATEWAY_OBJ_SUPPORT`) | First Uri-Path that matches `n<i>` is routed to the bridged device | mh:2339-2346; lwm2m_obj_gateway.c:151-191 |

### 3.4 Per-operation details

| Op | Content formats | Behaviour and edge cases | Cite |
|---|---|---|---|
| **Read** | Accept: 0/1541 text, 42 opaque, 60 CBOR, 11542/1542 TLV, 11543/1543 OMA-JSON, 110 SenML-JSON, 112 SenML-CBOR. The response Content-Format echoes the Accept value used. | Text, opaque and CBOR need path level ≥ 3, otherwise 4.05. Level 4 needs 1.1, otherwise 4.04. A missing instance gives 4.04. An object with no instances gives 2.05 with empty content. A single resource with no value, or a non-readable one, gives 4.04. Multi-resource reads skip unreadable or empty resources. A message overflow gives 5.00 (no Block2 unless block transfer is enabled). | mh:1638-1680,1790-1855,1702-1788; lwm2m_rw_plain_text.c:409-423; lwm2m_rw_opaque.c:116-130; lwm2m_rw_cbor.c:533-543 |
| **Write** (PUT/POST) | CF: same set as Read except link-format | Writes to non-existent instances **auto-create the instance** (and trigger an Update with payload). RO resources are silently skipped. Unknown optional resources are ignored on Create and BS-Write. A validation-callback failure gives 4.00. TLV at object level requires object-instance TLVs. Existing resource instances that are absent from the payload are **not** removed. SenML-JSON with Block1 gives 5.00. | mh:2022-2076,1067-1308; reg:1388-1411; tlv:836-1082; sj:1497-1501 |
| **Create** (POST /X) | TLV (needs an object-instance TLV carrying the id), SenML (absolute names) | **No Location-Path in the 2.01.** Instance id must come from the payload. A bare TLV resource list at level 1 gives 5.01. TLV with 0-length gives an empty instance. Creating over an existing id is not rejected (acts as Write). With ACL enabled, the creating server becomes owner. | tlv:1007-1056; mh:2566-2580 |
| **Write-Composite** (iPATCH) | 110, 112 | Block1 gives 5.01. Names are absolute. | mh:2197-2229 |
| **Execute** | payload passed raw as args | No `execute_cb` gives 4.04. FW Update `/5/0/2` when not Downloaded gives 4.05. | mh:2284-2309; fw:399-427 |
| **Delete** (DM) | | `/0/*` and `/3/*` give 4.05. **The path level is not validated**: the client deletes instance `(obj, obj_inst_id)` for any level. A level-1 DELETE `/X` therefore removes `/X/0`, and a level-3 DELETE removes the whole instance. Triggers an Update with payload. | mh:1610-1636 |
| **Bootstrap-Delete** | | Level > 2 gives 4.05. `/` or `/X` deletes everything except the BS Security instance and `/3`. Deleting those individually gives 4.05. | eng:296-360 |
| **Discover** | Accept 40 | DM: level 0 or `/0` gives 4.05. Object level: `</X>;ver;attrs,</X/i>,</X/i/r>…`. Instance level: `</X/i>;attrs,</X/i/r>[;dim=n][;attrs]…`. Resource level: `</X/i/r>;dim;attrs` (inherited from obj and inst) plus `</X/i/r/ri>;attrs` in 1.1. Nothing matched gives 4.04. Attrs: `pmin`/`pmax` as ints, `gt`/`lt`/`st` as floats with 4 decimals. | mh:1890-2020; lf:149-232,320-620 |
| **BS-Discover** | Accept 40 | Level > 1 gives 4.05. Output is `</>;lwm2m=…`, then `</X>[;ver]`, then `</X/i>`. `/0` and `/1` instances get `;ssid=N`; the BS Security instance has no ssid. | mh:1906-1908; lf:63-65,396-412,257-318 |
| **Read-Composite** (FETCH) | 110, 112. **The request body is parsed with the format given by Accept, not Content-Format.** | Up to `LWM2M_COMPOSITE_PATH_LIST_SIZE` (6) paths. `/` reads all objects except `/0`. Overlapping paths are de-duplicated. No valid path gives 5.00. Any `/0` path outside BS gives 4.01. | mh:1682-1700,2556,3234-3328,3463-3492; sj:1576-1675 |
| **Observe** (GET + Observe:0) | Accept as for Read. Notifications reuse this format. | Token required (1-8 bytes), otherwise 5.00. The response carries `Observe: 0`. **Only one observation per path**: re-observing the same path replaces the token and cancels any in-flight notify. The observer is added *before* the read, so if the read then fails (e.g. 4.04) the observation remains. Pool size is `LWM2M_ENGINE_MAX_OBSERVER` (default 10); when it is full: 5.00. GET with Accept 40 and Observe is treated as plain Discover. | mh:2527-2551; obs:1733-1756,879-940,894-897 |
| **Cancel** (GET + Observe:1) | | Matched by token. With `LWM2M_CANCEL_OBSERVE_BY_PATH` (default n) it falls back to path matching. An unknown token is still answered with 2.05 and a normal Read. A **RST** to a Notify also cancels. | obs:1756-1772,1130-1155; mh:3032-3041 |
| **Observe-Composite** (FETCH + Observe) | 110, 112 | Path list taken from the body. The response is a composite read. Cancel requires the same token **and** an identical path list. | obs:965-1030,1066-1102 |
| **Write-Attributes** | Uri-Query only | Only `pmin`, `pmax`, `gt`, `lt` and `st` are recognised. **`epmin`, `epmax`, `edge`, `con`, `hqmax` and unknown keys are silently ignored.** At most 5 query options are read. A key with no value unsets that attribute. `gt`/`lt`/`st` on an object or instance, or on a non-numeric resource, give 4.00. Negative or invalid numbers, `pmin > pmax`, `lt > gt`, and `lt + 2·st > gt` give 4.00. `/0` gives 4.04. Pool is `LWM2M_NUM_ATTR` (20); value tracking is limited to `LWM2M_MAX_NOTIFIED_NUMERICAL_RES_TRACKED` (4). Changing pmin/pmax reschedules observers. | obs:1462-1714,86 |
| **Block1 inbound** (any Write) | | Context keyed by path. There are `LWM2M_NUM_BLOCK1_CONTEXT` (3) contexts, each with a 30 s idle timeout. The client adopts the server's SZX from block 0. A non-last block shorter than the block size gives 4.13. An out-of-order block gives 4.08. Re-sending an already handled block gets 2.31 with the original Block1 echoed. Intermediate blocks get 2.31 plus Block1. The last block gets 2.04 plus Block1. | mh:2078-2195,120-208,71 |
| **Block2 for responses** | | Only with `LWM2M_COAP_BLOCK_TRANSFER=y`. Only **one** Block2 transfer can be in progress. Any new request without Block2 aborts it. Request-Tag is not used. The client honours a smaller SZX that the server asks for. | mh:2690-2741,2899-2909 |

---

## 4. Outbound: Notify and Send

### 4.1 Notify

| Item | Behaviour | Cite |
|---|---|---|
| Type | **Always CON.** NON is never used. | mh:3169 |
| Code, token, MID | 2.05, observation token, new MID | mh:3170-3173 |
| Observe option | Counter starts at 0 (initial response). Each notify increments it, so the first notify is 1. | obs:55,1740-1741; mh:3190-3192 |
| Format | The Accept of the Observe request, otherwise the default from 3.3. Composite observations use the composite read encoder. | obs:753; mh:3198-3205 |
| Concurrency | One in-flight notify per observation. If a notify is already pending, the next is delayed in 100 ms steps. At most one new notify per engine loop, and only when the send queue is empty and the client is registered. | eng:595-638,73,837-862 |
| Defaults | pmin and pmax come from `/1/x/2` and `/1/x/3` (Kconfig `LWM2M_SERVER_DEFAULT_PMIN/PMAX`, default **0/0**). Attributes are inherited obj → inst → res → res-inst. If pmax < pmin, pmax is ignored. | obs:351-445; srv:177-187; K:208-223 |
| pmax | Periodic notify at `last + pmax`. `pmax=0` means **no periodic notifications**. Composite observations use the minimum non-zero pmin and pmax across their paths. | obs:736-750,1780-1797,447-485 |
| pmin | A value change schedules a notify at `last_notify + pmin`, using the larger of the observation pmin and the resource pmin. With pmin 0 the notify is immediate. | obs:626-693 |
| gt/lt/st | Evaluated only on value change, at resource level, for numeric resources. `st`: notify if \|new−last_notified\| ≥ st. `gt`/`lt`: notify on a crossing in either direction. Periodic pmax notifies ignore these conditions. | obs:552-624 |
| epmin/epmax | **Not supported** | obs:85-86,227-243 |
| Change triggers | Any local `lwm2m_set_*` or server write to a readable resource | reg:738; mh:1303-1305 |
| ACK | Clears the in-flight flag. Pending cached time-series data triggers the next notify. | mh:3010-3058 |
| RST | Removes the observation | mh:3032-3041 |
| Timeout (CoAP retries exhausted) | `NOTIFY_TIMEOUT` callback, then **full re-registration**, which clears all observations | mh:2960-2982; rd:1811-1826 |
| Queue mode | Buffered while RX-off and sent after the Update gets its 2.04 (see 6.5) | mh:746-776 |
| Time-series | With `LWM2M_RESOURCE_DATA_CACHE_SUPPORT`, SenML records carry `bt`/`t`. If the message is too big, it is rebuilt with at most 20 entries. | mh:3080-3116; lwm2m_registry.h:220 |

### 4.2 Send (1.1 only)

| Item | Behaviour | Cite |
|---|---|---|
| Request | CON `POST /dp`, 8-byte token | mh:3637-3668,73 |
| Content-Format | 112 if SenML-CBOR is enabled, else 110. Neither enabled means Send is unsupported. | mh:3604-3611 |
| Payload | Composite read of up to `LWM2M_COMPOSITE_PATH_LIST_SIZE` (6) paths, de-duplicated | mh:3597-3622,3264-3328 |
| Preconditions | Registered and not muted (`/1/x/23`) | mh:3586-3594 |
| Response | 2.04 counts as success. Any other code is a failure and is **not retried**. | mh:3495-3521 |
| Timeout | Status callback, then **full re-registration** | mh:3523-3530 |
| Cache | When time-series data overflows, several `/dp` messages are sent back to back | mh:3689-3701 |
| Scheduler | `LWM2M_SEND_SCHEDULER` adds vendor objects 10523/10524 (1.1 and cache required) | K:263-271; send_scheduler/lwm2m_obj_send_scheduler.c:28-29 |

### 4.3 What happens around an Update

- After the Update gets **2.04** (and also after Register 2.01), messages buffered in queue mode are released (rd:218-225; eng:234-253). The client does not re-send notifications by itself.
- Any **full Register** (initial, after an Update failure or timeout, after a Notify or Send timeout, or after waking with DTLS and socket close) runs `lwm2m_engine_context_close` first. All observations are lost, so **the server must assume all observations are cancelled on every Register** (rd:1067-1093; mh:442-475).

---

## 5. Content formats

| Format | CF | Read | Write | Kconfig (default) | Notes | Cite |
|---|---|---|---|---|---|---|
| Plain text | 0, 1541 | yes | yes | always built | Single resource or res-inst only. Bool as `1`/`0`, objlnk as `a:b`, time as `%lld`. | mh:891-893,944-947; lwm2m_rw_plain_text.c:127-187,409-423 |
| Opaque | 42 | yes | yes | always built | Single resource only. Supports Block1 streaming. | lwm2m_rw_opaque.c; mh:887-889 |
| Link-format | 40 | Discover / Register only | no | always built | | lf |
| OMA TLV | 11542, 1542 (old) | yes | yes | `LWM2M_RW_OMA_TLV_SUPPORT` (implied by 1.0) | Supports Block1 across resources. Res-inst TLV needs 1.1. | tlv:974-1082; K:484-487 |
| OMA JSON | 11543, 1543 (old) | yes | partial | `LWM2M_RW_JSON_SUPPORT` (n) | Opaque decode returns `EOPNOTSUPP` (5.00) | lwm2m_rw_json.c:803-810; K:489-493 |
| SenML-JSON | 110 | yes | yes, no Block1 | `LWM2M_RW_SENML_JSON_SUPPORT` (n, needs BASE64 and JSON) | `bn` = `/o/i/`, `n` = `r` or `r/ri`. Labels `v`, `vb`, `vs`, `vd` (base64url, no padding), `vlo`, `bt`, `t`. | sj:145-157,309,882-915,1497-1501 |
| SenML-CBOR | 112 | yes | yes, Block1 continuation | `LWM2M_RW_SENML_CBOR_SUPPORT` (n, needs ZCBOR_CANONICAL) | Integer keys bn=-2, bt=-3, n=0, v=2, vs=3, vb=4, vd=8, t=6; `vlo` is a text key. Max records `LWM2M_RW_SENML_CBOR_RECORDS` (30). | lwm2m_senml_cbor_types.h:23-32; lwm2m_senml_cbor_encode.c:113-124; K:509-522 |
| Raw CBOR | 60 | single value | single value | `LWM2M_RW_CBOR_SUPPORT` (implied by 1.1) | Decoding time from a string returns ENOTSUP | lwm2m_rw_cbor.c:325-341,533-574 |
| **LwM2M-CBOR** | 11544 | **no** | **no** | | Not implemented | mh:12-27 |
| EXI | 47 | no | no | | Defined only | mh:15 |

Defaults with no Accept are listed in 3.3. Bootstrap `pct` and the registration `ct` are covered in 2.5 and 1.3.

---

## 6. Transport

### 6.1 CoAP

| Parameter | Value | Cite |
|---|---|---|
| Token length (client-originated) | 8 bytes, random | mh:623-625; coap.h:225,629-634 |
| Observation token | 1-8 bytes accepted; 0 is rejected | obs:894-897; lwm2m_observation.h:13 |
| Max outbound CoAP message | `LWM2M_COAP_MAX_MSG_SIZE`: **1232** without DTLS, **1195** with DTLS, **1187** with DTLS and CID | K:308-318; lwm2m_object.h:494 |
| Inbound receive buffer | `NET_IPV6_MTU` (1280) | eng:703-711 |
| Block size | `LWM2M_COAP_BLOCK_SIZE` default **512** (64-1024) | K:298-306 |
| Outbound block-wise (Block1 requests, Block2 responses) | `LWM2M_COAP_BLOCK_TRANSFER` (**n**, experimental). Encode buffer `LWM2M_COAP_ENCODE_BUFFER_SIZE` 1024, `LWM2M_NUM_OUTPUT_BLOCK_CONTEXT` 3. Adds an **ETag** (sys_hash32) if `SYS_HASH_FUNC32`. Uses a new token per Block1 request. Expects 2.31 with Block1 M=1. If the server picks a different SZX the client logs it and continues with that size. | K:80-87,392-415; mh:278-438,2819-2867 |
| Retransmission | Zephyr CoAP pending: `COAP_INIT_ACK_TIMEOUT_MS`† 2000, `COAP_RANDOMIZE_ACK_TIMEOUT`† y with `COAP_ACK_RANDOM_PERCENT`† 150, `COAP_MAX_RETRANSMIT`† 4, `COAP_BACKOFF_PERCENT`† 200. Kconfig names confirmed by the samples. | eng:366-415,756-758; coap.h:409-422; samples overlay-hl7800.conf, overlay-swir_hl78xx_ev_kit_ntn.conf |
| Empty ACK then separate response | Supported. The client waits for the separate response and ACKs a CON response. | mh:2786-2817 |
| NSTART | **Not enforced.** Up to `LWM2M_ENGINE_MAX_PENDING`+1 (6) outstanding CONs, `MAX_REPLIES`+1 (6) replies, `MAX_MESSAGES` (10) messages. RD messages are serialized (one at a time) and notifies are limited to one per observation. | lwm2m.h:224-225; K:292-341 |
| Engine loop | Tickless (eventfd) when `ZVFS_EVENTFD_MAX > 1`, otherwise a 500 ms poll | K:162-174; eng:72,866-875 |
| Socket | **Connected** UDP socket: responses must come from the server's IP and port. Non-blocking. | eng:1229-1247 |

### 6.2 DTLS

| Item | Behaviour | Cite |
|---|---|---|
| Enable | `LWM2M_DTLS_SUPPORT` (n). Socket uses `IPPROTO_DTLS_1_2` (DTLS 1.2 only). | K:72-74; eng:140-143 |
| Mode (`/0/x/2`) | 0 = PSK supported. 2 = X.509 supported. 3 = NoSec supported (but `coaps://` with NoSec is an error). **1 = RPK not supported (EOPNOTSUPP)**, and 4 = Cert-EST is not supported either. | eng:1047-1062; lwm2m.h:1645-1649 |
| PSK | Identity is `/0/x/3`, key is `/0/x/5` (binary) | eng:998-1011 |
| X.509 | Client cert `/0/x/3`, private key `/0/x/5`, and `/0/x/4` loaded **as the CA certificate**. PEM is detected by `-----BEGIN`, otherwise DER. | eng:1013-1033,943-996 |
| Key buffers | `LWM2M_SECURITY_KEY_SIZE` default **16 bytes** for identity, server PK and secret key. Larger bootstrap writes are not rejected cleanly (the buffer is reused chunk by chunk), so provisioning must fit. Samples use 32 (PSK) and 2048 (cert). | sec:57-64; mh:1025-1058; samples overlay-dtls.conf, overlay-dtls-cert.conf |
| Ciphersuites offered | PSK: `TLS_PSK_WITH_AES_128_CCM_8`. Cert: `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8` and `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`. If setting the list fails, this is only logged and the mbedTLS defaults apply (the sample enables PSK_AES_128_GCM_SHA256). | eng:1064-1071,1155-1175 |
| Peer verify | **REQUIRED only in cert mode when the URI host was resolved by DNS** (SNI is also set via `TLS_HOSTNAME`). An IP-literal URI in cert mode and PSK mode both use `PEER_VERIFY_NONE`. | eng:1117-1153; mh:3435-3439 |
| Session resumption | `LWM2M_TLS_SESSION_CACHING` (n) sets `TLS_SESSION_CACHE_ENABLED` | K:120-123; eng:1093-1103 |
| Connection ID | `LWM2M_DTLS_CID` (default y if `MBEDTLS_SSL_DTLS_CONNECTION_ID`) sets `ZSOCK_TLS_DTLS_CID = 1` (SUPPORTED). The client sends a **zero-length CID**, so it receives without a CID, while the **server should assign a CID** for the client's uplink records. A failure here is non-fatal. | K:125-130; eng:1104-1115; socket.h:205-216,309-312 |
| Overrides | `ctx->load_credentials` and `ctx->set_socketoptions` callbacks (e.g. DTLS handshake timeouts in the sample) | lwm2m.h:261-273; eng:1191-1215; samples/net/lwm2m_client/src/lwm2m-client.c:345-400 |
| Offloaded TLS (nRF91 and similar) | **This tree has no vendor-specific code.** Offloaded stacks plug in through the socket API and the two callbacks above, and through `set_socket_state` hints (ONGOING, ONE_RESPONSE, LAST, NO_DATA) used for RAI and PSM. In this tree, the only offload example is the HL78xx overlay. | lwm2m.h:199-204,334; eng:652-699; samples/net/lwm2m_client/pinnacle_100-hl78xx.overlay:24-25 |

### 6.3 Socket close and reopen

| Event | Socket and DTLS | Next message | Cite |
|---|---|---|---|
| Network error (each retry) | Closed, then a new handshake | Register, or Update if still within the lifetime | rd:1338,1414-1435 |
| Update timeout | Closed, then a new handshake | Register | rd:614-623,1083-1088 |
| Update 4.xx | Kept (same session) | Register | rd:1036-1043 |
| Register retry with socket open | Kept unless `close_socket` | Register | rd:1083-1093 |
| Bootstrap finish | Closed | Register to the DM server (new connection) | rd:872-887 |

### 6.4 NAT

The client has no keep-alive or NAT detection beyond the periodic Update. An expired NAT binding shows up as CoAP timeouts, which lead to re-registration with a new handshake. To keep uplink working across port changes without a re-handshake, use DTLS CID (6.2). The server must also accept an Update or Register from a changed address (rd:1414-1435).

### 6.5 Queue mode (`LWM2M_QUEUE_MODE_ENABLED`, default n)

| Item | Behaviour | Cite |
|---|---|---|
| Binding | 1.0 sends `b=UQ`. 1.1 sends `b=U&Q`. | reg:1131-1151; rd:975-1002 |
| RX-off | `QUEUE_MODE_UPTIME` (default **93 s**) after the last TX, provided there is no ongoing traffic (socket-state hint) | K:103-111; rd:1164-1175 |
| Idle socket choice (default = first entry, **`CLOSE_SOCKET_AT_IDLE`**) | `CLOSE_SOCKET_AT_IDLE`: close at RX-off. `SUSPEND_SOCKET_AT_IDLE`: stop polling, close on resume. `STOP_POLLING_AT_IDLE`: keep the socket and stop polling. `LISTEN_AT_IDLE`: keep the socket and keep listening. | K:145-160; rd:252-259; eng:186-232 |
| Wake (next Update time, or a local Notify or Send) | If (SUSPEND and session caching) or STOP_POLLING or LISTEN or no DTLS: an **Update** (or nothing, with `NO_MSG_BUFFERING`). **Otherwise (DTLS with CLOSE or SUSPEND and no cache): a new handshake and full Register**, which drops all observations. | rd:1778-1809 |
| Buffering | Notify and Send are held in `queued_messages` until the Update or Register succeeds, then sent with t0 reset. `LWM2M_QUEUE_MODE_NO_MSG_BUFFERING` (n, experimental) sends them immediately. | mh:746-776; eng:234-253; K:113-118 |
| Server implication | Server-originated requests only arrive while RX is on: within `QUEUE_MODE_UPTIME` of the client's last TX (default 93 s, 20-30 s in the samples and tests). | samples overlay-queue.conf; tests/net/lib/lwm2m/interop/prj.conf |
| Suspend and resume (API) | Resume within the lifetime goes to RX-off and then the wake logic above. Resume after the lifetime has expired goes to Register. | rd:1656-1738 |

---

## 7. Firmware Update object 5 and built-in objects

### 7.1 Object `/5` v1.0 (`LWM2M_FIRMWARE_UPDATE_OBJ_SUPPORT`, default **y**)

| Item | Behaviour | Cite |
|---|---|---|
| Resources | 0 Package (W, opaque), 1 Package URI (RW string, `LWM2M_SWMGMT_PACKAGE_URI_LEN` 128), 2 Update (E), 3 State, 5 Result, 6 and 7 name and version (opt, empty), 8 protocol support (opt multi, empty unless the app sets it), 9 Delivery Method (2 = both when pull is enabled, 1 = push only) | fw:32-46,64-72,457-475,508-512 |
| Instances | 1. Multiple instances need `LWM2M_FIRMWARE_UPDATE_OBJ_SUPPORT_MULTIPLE` (experimental) and `…_INSTANCE_COUNT`. | fw:25-29; K:628-639 |
| **Push** | Write `/5/0/0` (usually CF 42 with Block1). Idle goes to Downloading; the last block moves it to Downloaded. Write-callback errors: ENOMEM gives result 3, ENOSPC gives result 2 and **4.13**, EFAULT gives result 5, ENOMSG gives result 6. Writing an empty package (0 bytes or `\0`) while Downloaded resets to Idle. A non-empty write while Downloaded or Updating gives 4.05. | fw:236-298 |
| **Pull** (`LWM2M_FIRMWARE_UPDATE_PULL_SUPPORT`, default y) | Write `/5/0/1`. **Block1 writes to the URI give 4.13.** A non-empty URI while Idle starts a download. An empty URI while Downloaded resets. Any other state is ignored (2.04). | fw:300-337; K:641-648 |
| Pull transfer | A separate socket and context. Only `coap://` and `coaps://`. Default ports 5683/5684 (`LWM2M_FIRMWARE_PORT_NONSECURE/SECURE`). CON GET with the URL **path** split into Uri-Path. **The Uri-Query of the package URI is dropped.** Sends Block2 (num, `LWM2M_COAP_BLOCK_SIZE`) and **Size2: 0**. The **same 8-byte token** is reused for every block. Only 2.05 is accepted (anything else gives result 4). The client follows the server's block size. Duplicate blocks are ignored. A CoAP timeout gives result 4 (connection lost). | pull:94-217,219-358,360-418; lwm2m_obj_firmware_pull.c:19-53 |
| Pull DTLS | Uses the generic credential loader on the pull context (`firmware_ctx`, which is zero-initialised, so Security instance 0 and tls_tag 0) unless the app sets `lwm2m_pull_context_set_sockopt_callback` | pull:43,396-397,463-466; eng:1191-1215 |
| CoAP proxy | `LWM2M_FIRMWARE_UPDATE_PULL_COAP_PROXY_SUPPORT` (n) with `…_PROXY_ADDR`. Sends GET to the proxy with Uri-Path `coap2coap` (coap URIs) or `coap2http` (http URIs) and **Proxy-Uri** = the full package URI. Size2 is not sent. | pull:27-32,126-142,189-195,375-385; K:650-660 |
| Execute `/5/0/2` | Only when State is Downloaded (otherwise 4.05). Moves to Updating and calls the app callback. A callback error gives result 5 or 8 (still 2.04). | fw:399-427 |
| Notify | State and Result changes go through `lwm2m_set_u8`, so observers of `/5/0/3` and `/5/0/5` are notified | fw:140-142,226; reg:738 |
| SW Mgmt `/9` | Uses the same pull context for its own package URI | lwm2m_obj_swmgmt.c:655-690 |

### 7.2 Built-in objects

Core objects are listed in reg:62-86. "Core" here affects the `ver=` reporting rule in 1.3.

| ID | Object | Version | Kconfig (default) | Cite |
|---|---|---|---|---|
| 0 | Security | 1.0 or 1.1 (follows protocol) | always | sec:20-31; K:38-51 |
| 1 | Server | 1.0 or 1.1 | always | srv:23-30; K:53-66 |
| 2 | Access Control | 1.0 | `LWM2M_ACCESS_CONTROL_ENABLE` (n), instances 50 | lwm2m_obj_access_control.c:57-58; K:569-579 |
| 3 | Device | **1.0** (even in 1.1) | always | lwm2m_obj_device.c:28-29 |
| 4 | Connectivity Monitoring | 1.0, 1.2 (default with 1.1) or 1.3 | `LWM2M_CONN_MON_OBJ_SUPPORT` (n) | lwm2m_obj_connmon.c:21-29; K:581-603 |
| 5 | Firmware Update | 1.0 | `LWM2M_FIRMWARE_UPDATE_OBJ_SUPPORT` (**y**) | fw:22-23; K:621-625 |
| 6 | Location | 1.0 | `LWM2M_LOCATION_OBJ_SUPPORT` (n) | lwm2m_obj_location.c:20-21 |
| 9 | Software Management | 1.0 | `LWM2M_SWMGMT_OBJ_SUPPORT` (n) | lwm2m_obj_swmgmt.c:26-27 |
| 16 | Portfolio | 1.0 | `LWM2M_PORTFOLIO_OBJ_SUPPORT` (n) | lwm2m_obj_portfolio.c:20-21 |
| 19 | BinaryAppDataContainer | 1.0 | `LWM2M_BINARYAPPDATA_OBJ_SUPPORT` (n) | lwm2m_obj_binaryappdata.c:28-29 |
| 20 | Event Log | 1.0 | `LWM2M_EVENT_LOG_OBJ_SUPPORT` (n) | lwm2m_obj_event_log.c:28-29 |
| 25 | Gateway | **2.0** | `LWM2M_GATEWAY_OBJ_SUPPORT` (n, experimental) | lwm2m_obj_gateway.c:29-30 |
| 3300 | Generic Sensor | 1.0 or 1.1 | `LWM2M_IPSO_GENERIC_SENSOR` | ipso_generic_sensor.c:27-33 |
| 3303 | Temperature | 1.0 or 1.1 | `LWM2M_IPSO_TEMP_SENSOR` | ipso_temp_sensor.c:27-33 |
| 3304 | Humidity | 1.0 or 1.1 | `LWM2M_IPSO_HUMIDITY_SENSOR` | ipso_humidity_sensor.c:22-28 |
| 3311 | Light Control | 1.0 | `LWM2M_IPSO_LIGHT_CONTROL` | ipso_light_control.c:27-28 |
| 3313 | Accelerometer | 1.0 or 1.1 | `LWM2M_IPSO_ACCELEROMETER` | ipso_accelerometer.c:25-31 |
| 3314 | Magnetometer | 1.0 | `LWM2M_IPSO_MAGNETOMETER` | ipso_magnetometer.c:25-26 |
| 3316 | Voltage | 1.0 or 1.1 | `LWM2M_IPSO_VOLTAGE_SENSOR` | ipso_voltage_sensor.c:26-32 |
| 3317 | Current | 1.0 or 1.1 | `LWM2M_IPSO_CURRENT_SENSOR` | ipso_current_sensor.c:25-31 |
| 3323 | Pressure | 1.0 or 1.1 | `LWM2M_IPSO_PRESSURE_SENSOR` | ipso_pressure_sensor.c:22-28 |
| 3333 | Time | 1.0 | `LWM2M_IPSO_TIME` | ipso_time.c:26-27 |
| 3338 | Buzzer | 1.0 or 1.1 | `LWM2M_IPSO_BUZZER` | ipso_buzzer.c:25-32 |
| 3340 | Timer | 1.0 | `LWM2M_IPSO_TIMER` | ipso_timer.c:25-26 |
| 3342 | On/Off Switch | 1.0 or 1.1 | `LWM2M_IPSO_ONOFF_SWITCH` | ipso_onoff_switch.c:25-31 |
| 3347 | Push Button | 1.0 or 1.1 | `LWM2M_IPSO_PUSH_BUTTON` | ipso_push_button.c:25-31 |
| 3411 | uCIFI Battery | 1.0 | `LWM2M_UCIFI_BATTERY` | ucifi_battery.c:28-29 |
| 3412 | uCIFI LPWAN | 1.0 | `LWM2M_UCIFI_LPWAN` | ucifi_lpwan.c:27-28; ucifi_lpwan.h:9 |
| 3435 | Filling Level | 1.0 | `LWM2M_IPSO_FILLING_SENSOR` | ipso_filling_sensor.c:26-27 |
| 10523, 10524 | Send-scheduler control and rules | 1.0 | `LWM2M_SEND_SCHEDULER` | send_scheduler/lwm2m_obj_send_scheduler.c:28-29 |

IPSO objects default to version 1.0 (`…_VERSION_1_0`) under `LWM2M_IPSO_SUPPORT`, and each defaults to 1 instance (Kconfig.ipso). The OSCORE object 21 is listed but not implemented (reg:81-82).

---

## 8. Kconfig options that affect the wire

| Option | Default | Effect | Cite |
|---|---|---|---|
| `LWM2M_VERSION_1_0` / `_1_1` | 1.0 | `lwm2m=`, formats, Send, `Q`, `pct` | K:21-36 |
| `LWM2M_SECURITY_OBJECT_VERSION_1_x` / `LWM2M_SERVER_OBJECT_VERSION_1_x` | follows version | `ver=`, available resources | K:38-66 |
| `LWM2M_DTLS_SUPPORT` | n | coaps | K:72 |
| `LWM2M_DNS_SUPPORT` | y if DNS_RESOLVER | hostname URIs, SNI and peer verify | K:76-78 |
| `LWM2M_COAP_BLOCK_TRANSFER` | n | outbound Block1 and Block2 | K:80 |
| `LWM2M_CANCEL_OBSERVE_BY_PATH` | n | cancel fallback by path | K:89 |
| `LWM2M_QUEUE_MODE_ENABLED` | n | `b=UQ` / `Q`, RX-off | K:98 |
| `LWM2M_QUEUE_MODE_UPTIME` | 93 | RX window, in seconds | K:103 |
| `LWM2M_QUEUE_MODE_NO_MSG_BUFFERING` | n | send immediately, no Update on wake | K:113 |
| `LWM2M_TLS_SESSION_CACHING` | n | DTLS resumption, Update on wake | K:120 |
| `LWM2M_DTLS_CID` | y if mbedTLS CID | CID extension | K:125 |
| `LWM2M_RD_CLIENT_SUPPORT_BOOTSTRAP` | n | `/bs` flow, BS ops | K:132 |
| `LWM2M_ENGINE_ALWAYS_REPORT_OBJ_VERSION` | n | `ver=` on every object | K:137 |
| `LWM2M_RD_CLIENT_{CLOSE_SOCKET,SUSPEND_SOCKET,STOP_POLLING,LISTEN}_AT_IDLE` | CLOSE (first in the choice) | RX-off socket handling; Update vs Register on wake | K:145-160 |
| `LWM2M_TICKLESS` / `LWM2M_INTERVAL` | tickless if eventfd > 1 | timing granularity | K:162-174 |
| `LWM2M_SERVER_DEFAULT_SSID` | 101 | sample SSID | K:184 |
| `LWM2M_PEER_PORT` | 5683 | default server port | K:190 |
| `LWM2M_FIRMWARE_PORT_NONSECURE` / `_SECURE` | 5683 / 5684 | pull ports | K:196-206 |
| `LWM2M_SERVER_DEFAULT_PMIN` / `PMAX` | 0 / 0 | notify defaults | K:208-223 |
| `LWM2M_RD_CLIENT_MAX_RETRIES` | 5 | retries; disable time = n × 247 s | K:225; rd:76 |
| `LWM2M_RESOURCE_DATA_CACHE_SUPPORT` | n | time-series SenML | K:235 |
| `LWM2M_MAX_CACHED_RESOURCES` | 4 | | K:243 |
| `LWM2M_SEND_SCHEDULER` | n | objects 10523/10524 | K:263 |
| `LWM2M_ENGINE_MAX_MESSAGES` | 10 | concurrent messages | K:292 |
| `LWM2M_COAP_BLOCK_SIZE` | 512 | block SZX | K:298 |
| `LWM2M_COAP_MAX_MSG_SIZE` | 1232 / 1195 / 1187 | outbound size | K:308 |
| `LWM2M_ENGINE_VALIDATION_BUFFER_SIZE` | 64 | max size of validated writes | K:320 |
| `LWM2M_ENGINE_MAX_PENDING` / `MAX_REPLIES` | 5 / 5 (+1) | outstanding CONs | K:331-341 |
| `LWM2M_ENGINE_MAX_OBSERVER` | 10 (5-200) | observations (×3 paths in 1.1) | K:343; eng:75-80 |
| `LWM2M_RD_CLIENT_ENDPOINT_NAME_MAX_LENGTH` | 33 | ep and registration ID length | K:351 |
| `LWM2M_SECURITY_KEY_SIZE` | 16 | PSK id and key, cert buffers | K:357 |
| `LWM2M_SECURITY_DTLS_TLS_CIPHERSUITE_MAX` | 5 | `/0/x/16` instances | K:364 |
| `LWM2M_SECURITY_INSTANCE_COUNT` / `SERVER_INSTANCE_COUNT` | 1 (2 with BS) | | K:374-390 |
| `LWM2M_COAP_ENCODE_BUFFER_SIZE` | 1024 | max block-wise body | K:393 |
| `LWM2M_NUM_OUTPUT_BLOCK_CONTEXT` | 3 | | K:400 |
| `LWM2M_NUM_BLOCK1_CONTEXT` | 3 | concurrent inbound Block1 | K:417 |
| `LWM2M_SWMGMT_PACKAGE_URI_LEN` | 128 | `/5/0/1` max length | K:424 |
| `LWM2M_COMPOSITE_PATH_LIST_SIZE` | 6 | Composite and Send paths | K:428 |
| `LWM2M_NUM_ATTR` | 20 | attribute pool | K:466 |
| `LWM2M_MAX_NOTIFIED_NUMERICAL_RES_TRACKED` | 4 | gt/lt/st tracking | K:473 |
| `LWM2M_RW_OMA_TLV_SUPPORT` | implied by 1.0 | | K:484 |
| `LWM2M_RW_JSON_SUPPORT` | n | | K:489 |
| `LWM2M_RW_SENML_JSON_SUPPORT` | n | | K:496 |
| `LWM2M_RW_CBOR_SUPPORT` | implied by 1.1 | | K:503 |
| `LWM2M_RW_SENML_CBOR_SUPPORT` | n | | K:509 |
| `LWM2M_RW_SENML_CBOR_RECORDS` | 30 | | K:516 |
| `LWM2M_ENGINE_DEFAULT_LIFETIME` | 30 (≥ 15) | `lt` default and floor | K:526 |
| `LWM2M_UPDATE_PERIOD` | 0 | Update interval cap | K:535 |
| `LWM2M_SECONDS_TO_UPDATE_EARLY` | 10 | Update lead time | K:545 |
| `LWM2M_SERVER_BOOTSTRAP_ON_FAIL` | y | `/1/x/16` default | K:555 |
| `LWM2M_ACCESS_CONTROL_ENABLE` | n | object 2, ACL checks | K:569 |
| `LWM2M_CONN_MON_OBJ_SUPPORT` (+ `CONNMON_OBJECT_VERSION_*`) | n | object 4 | K:581-603 |
| `LWM2M_FIRMWARE_UPDATE_OBJ_SUPPORT` | y | object 5 | K:621 |
| `LWM2M_FIRMWARE_UPDATE_PULL_SUPPORT` | y | URI pull, delivery method 2 | K:641 |
| `LWM2M_FIRMWARE_UPDATE_PULL_COAP_PROXY_SUPPORT` / `_ADDR` | n / "" | Proxy-Uri pull | K:650-660 |
| `LWM2M_LOCATION_OBJ_SUPPORT`, `SWMGMT_OBJ_SUPPORT`, `PORTFOLIO_OBJ_SUPPORT`, `BINARYAPPDATA_OBJ_SUPPORT`, `EVENT_LOG_OBJ_SUPPORT`, `GATEWAY_OBJ_SUPPORT` | n | objects 6, 9, 16, 19, 20, 25 | K:664-769 |
| `LWM2M_GATEWAY_DEFAULT_DEVICE_PREFIX` | "n" | Uri-Path prefix `n0…` | K:745 |
| CoAP: `COAP_INIT_ACK_TIMEOUT_MS`†, `COAP_RANDOMIZE_ACK_TIMEOUT`†, `COAP_ACK_RANDOM_PERCENT`†, `COAP_MAX_RETRANSMIT`†, `COAP_BACKOFF_PERCENT`† | 2000, y, 150, 4, 200 | retransmission timing | coap.h:409-422 |
| `COAP_EXTENDED_OPTIONS_LEN(_VALUE)` | n (sample: y, 40) | longest option (Location-Path, query) accepted; Write-Attributes values otherwise limited to 12 bytes | obs:57-61; samples prj.conf |

### Server checklist from this profile

1. Expect `lt` of 30 s or more: the client raises anything lower to `LWM2M_ENGINE_DEFAULT_LIFETIME` (default 30 s).
2. Return Location-Path `rd/<id>` with `<id>` of 32 bytes or less.
3. Treat every Register as resetting all observations.
4. Accept an Update after a new DTLS handshake from a new address.
5. Use CON-only notifications and expect them to ACK.
6. Never rely on a Location-Path in a Create response, and always put the instance id in the payload.
7. Send FETCH Read-Composite with Accept equal to Content-Format.
8. Leave `epmin` and `epmax` unused.
9. Support DTLS 1.2 PSK with `AES_128_CCM_8`, and server-assigned CID.
10. Keep FOTA URIs path-only (no query) and serve them block-wise with Block2.
11. In queue mode, hold downlink requests until the next uplink and deliver them within the UPTIME window.
