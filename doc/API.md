# Northbound API v1

Status: **design**. Nothing here is implemented yet. This is the contract for the HTTP API that applications, integrations and the hosted console use to manage devices through `lwm2md`.

It replaces nothing: `leshanapi` stays, only so the Zephyr interop suite runs unchanged. It is not a product API and new features do not go there.

## 1. Principles

- **One API for everyone.** The open-source server and the hosted console use the same API. The console has no private back door.
- **Devices are durable, sessions are not.** A device exists before its first registration and after its last. Its registration is a sub-resource that comes and goes.
- **Every downlink is an operation.** Read, Write, Execute and the rest create an Operation resource that is queued, sent and completed. A caller can wait for the result or come back later. Sleeping and offline devices need no special handling by the caller.
- **No content formats on the API.** Callers send and receive typed JSON values. The server picks TLV, SenML, LwM2M CBOR and so on per device, and a caller can force one when testing a device.
- **Events are a durable log.** Registration, notification, Send and operation events get sequence IDs, can be replayed, and are delivered over SSE and webhooks.
- **Multi-tenant from day one.** A single-tenant install has one implicit tenant called `default`.

## 2. Conventions

| Topic | Rule |
|---|---|
| Base URL | `https://<host>/v1`. Health and metrics live outside `/v1` (§18). |
| Encoding | JSON, UTF-8, `camelCase` field names. Unknown request fields are rejected (400), so typos fail. |
| Times | RFC 3339 strings in UTC with optional fraction, e.g. `2026-10-08T10:00:00.250Z`. |
| Durations | Integer seconds, with field names ending in `Seconds` (`ttlSeconds`, `lifetimeSeconds`). The LwM2M attribute names (`pmin`, `pmax`, ...) keep their spec names and are also seconds. |
| IDs | Server-generated IDs are prefixed ULIDs: `op_01J9...`, `obs_...`, `fw_...`, `job_...`, `evt_...`, `key_...`, `wh_...`. They sort by creation time. |
| Devices | A device is addressed by its LwM2M endpoint name, percent-encoded in the path: `/v1/devices/urn:imei:350000000000001`. A `/` in an endpoint name is sent as `%2F`. |
| LwM2M paths | In request and response bodies, never in URLs: `"/3/0/1"`. A gateway end device is addressed with a separate `prefix` field (§14). |
| Pagination | `?limit=` (default 50, max 500) and `?cursor=`. Responses are `{"items": [...], "nextCursor": "..."}`; no `nextCursor` means the last page. |
| Filtering | `?selector=` uses label selectors: `env=prod,region in (eu,us),!beta`. List endpoints also take the specific filters listed with them. |
| Idempotency | Every `POST` accepts an `Idempotency-Key` header. A retry with the same key within 24 h returns the first response and creates nothing. |
| Concurrency | Mutable resources carry an `ETag`. `PUT`, `PATCH` and `DELETE` accept `If-Match`; a mismatch is 412. |
| Partial updates | `PATCH` uses JSON Merge Patch (RFC 7396), `Content-Type: application/merge-patch+json`. |
| Errors | `application/problem+json` (RFC 9457), see below. |
| Versioning | `/v1` only changes in backward-compatible ways: new fields, new endpoints, new enum values. Clients must ignore unknown fields and treat unknown enum values as "other". A breaking change is `/v2`. |
| Contract | The machine-readable form is OpenAPI 3.1 at `GET /v1/openapi.json`, generated from the implementation. This document is the design it must match. |

### Errors

```json
{
  "type": "https://lwm2m.dev/errors/device-not-found",
  "title": "Device not found",
  "status": 404,
  "detail": "no device urn:imei:350000000000001 in tenant acme",
  "code": "device_not_found",
  "errors": [{"field": "values[2].value", "message": "not an integer"}]
}
```

`code` is the stable, machine-readable value; `type` is its URL form. `errors` lists field-level validation failures.

A device answering a request with an error code is **not** an HTTP error. The operation completes with `status: "failed"` and the CoAP code (§6.4); the HTTP call that fetched it returns 200. HTTP errors are only about the API call itself.

| HTTP | When |
|---|---|
| 400 | Malformed request, unknown field, invalid LwM2M path, a value that does not match the object model |
| 401 | Missing or invalid credentials |
| 403 | Authenticated, but the scope or role does not allow it |
| 404 | Unknown resource, or one that belongs to another tenant (never 403, so tenants cannot probe) |
| 409 | Conflicts with current state: a second firmware update on one device, deleting firmware that an update uses |
| 412 | `If-Match` mismatch |
| 422 | Valid JSON that the server refuses: invalid bootstrap config, weak PSK (BS-10) |
| 429 | Rate limited, with `Retry-After` |
| 501 | A feature that waits upstream, e.g. RPK credentials (`code: "rpk_unsupported"`) |

## 3. Authentication, authorisation and tenancy

Every `/v1` request carries `Authorization: Bearer <token>`. Two kinds of token are accepted:

- **API keys** for machines: opaque strings `lwm_<id>_<secret>`, created through §17.2. The server stores only a hash.
- **JWTs** for people, issued by an OIDC provider the deployment trusts. The server validates them against the issuer's JWKS. The issuer, audience and the claim names for tenant and roles are server configuration. The hosted console's identity provider is one such issuer.

The tenant is part of the credential, never of the URL. An API key belongs to one tenant; a JWT names its tenant in a claim. Every resource belongs to exactly one tenant, and nothing crosses tenants.

**Scopes.** API keys carry scopes; JWT roles map to the same scopes.

| Scope | Allows |
|---|---|
| `devices:read` | Devices, registrations, state, time series, models, observations, operations, events |
| `devices:write` | Create, change and delete devices; profiles |
| `operations:write` | Create and cancel operations and observations |
| `operations:raw` | The `raw` operation type (§6.3), separate because it bypasses all validation |
| `credentials:write` | Device credentials, trust anchors, EST settings. Secrets are write-only for everyone. |
| `bootstrap:write` | Bootstrap configs |
| `firmware:write` | Firmware artifacts and updates |
| `jobs:write` | Fleet jobs |
| `admin` | API keys, webhooks, settings, audit log; implies all of the above |
| `platform` | Tenant management (§17.1). Only for the operator of a multi-tenant deployment. |

| Role | Scopes |
|---|---|
| `viewer` | `devices:read` |
| `operator` | `viewer` plus `operations:write`, `firmware:write`, `jobs:write` |
| `admin` | `admin` |

**How a device reaches its tenant.** A device on the wire carries no tenant, so the server resolves it from the credential: a PSK identity or OSCORE context leads to a provisioned device and so to its tenant. An X.509 certificate leads to the tenant whose trust anchor (§9.3) issued it. Endpoint names are unique per deployment, because they are the registration key on the CoAP side. An unknown endpoint over NoSec has no tenant; it is accepted only when the deployment allows auto-provisioning into one tenant (§17.3).

## 4. Resources at a glance

| Resource | Path |
|---|---|
| Devices | `/v1/devices`, `/v1/devices/{ep}` |
| Registration | `/v1/devices/{ep}/registration` |
| Device model | `/v1/devices/{ep}/model` |
| Operations | `/v1/devices/{ep}/operations`, `/v1/operations/{id}` |
| Observations | `/v1/devices/{ep}/observations`, `/v1/observations/{id}` |
| State and time series | `/v1/devices/{ep}/state`, `/v1/devices/{ep}/timeseries` |
| Credentials | `/v1/devices/{ep}/credentials`, `/v1/trust-anchors`, `/v1/est` |
| Bootstrap | `/v1/devices/{ep}/bootstrap`, `/v1/devices/{ep}/bootstrap/credentials`, `/v1/devices/{ep}/bootstrap/sessions` |
| Firmware | `/v1/firmware`, `/v1/devices/{ep}/firmware-updates` |
| Fleet jobs | `/v1/jobs` |
| Device profiles | `/v1/device-profiles` |
| Gateway end devices | `/v1/devices/{ep}/end-devices` |
| Object models | `/v1/models` |
| Events | `/v1/events`, `/v1/devices/{ep}/events`, `/v1/webhooks` |
| Tenancy and access | `/v1/tenants`, `/v1/api-keys`, `/v1/settings`, `/v1/audit-log` |
| Server | `/v1/info`, `/v1/openapi.json`, `/healthz`, `/readyz`, `/metrics` |

## 5. Devices and registrations

### 5.1 Device

```json
{
  "endpoint": "urn:imei:350000000000001",
  "name": "Pump 7, hall B",
  "labels": {"env": "prod", "site": "rotterdam"},
  "profile": "dp_01J9...",
  "status": "registered",
  "lastSeenAt": "2026-10-08T09:58:12Z",
  "createdAt": "2026-09-01T12:00:00Z",
  "updatedAt": "2026-10-01T08:00:00Z",
  "registration": { "...": "the current registration, §5.3, or null" }
}
```

`status` is computed, never written:

| Status | Meaning |
|---|---|
| `never-seen` | Provisioned, never registered |
| `registered` | Registered and reachable |
| `sleeping` | Registered in queue mode and not reachable right now (QM-02); operations queue until it wakes |
| `offline` | Was registered; the registration ended (de-registered, expired or removed) |

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/devices` | List. Filters: `selector`, `status`, `profile`, `lwm2mVersion`, `binding`, `object` (has object ID, e.g. `object=5`), `q` (substring of endpoint or name). Sort: `sort=endpoint\|-lastSeenAt\|createdAt`. |
| `POST` | `/v1/devices` | Create (pre-provision). Body: `endpoint` (required), `name`, `labels`, `profile`, and optionally `credentials` (§9.1) and `bootstrap` (§10) to provision everything in one call. |
| `GET` | `/v1/devices/{ep}` | Get, with the current registration embedded |
| `PATCH` | `/v1/devices/{ep}` | Change `name`, `labels`, `profile` |
| `DELETE` | `/v1/devices/{ep}` | Delete the device, its credentials, bootstrap config, observations, queued operations and state. An active registration is removed first (reason `removed`). Time series and events are kept until their retention ends. |

### 5.2 Auto-created devices

A registration from an endpoint that has no device record creates one when §17.3 allows it. The device gets the tenant's default profile and the label `provisioning=auto`.

### 5.3 Registration

The current session of a device: what it sent in its last Register and Update (C §6.2).

```json
{
  "id": "rd_7f3a",
  "lwm2mVersion": "1.2",
  "lifetimeSeconds": 86400,
  "binding": "UQ",
  "queueMode": true,
  "sms": "+31600000000",
  "profileIds": ["oma:1"],
  "rootPath": "",
  "objects": [
    {"id": 3, "version": "1.2", "instances": [0]},
    {"id": 3303, "version": "1.1", "instances": [0, 1]}
  ],
  "contentFormats": ["senml-cbor", "lwm2m-cbor", "tlv"],
  "identity": {"mode": "psk", "pskIdentity": "pump-7"},
  "transport": "U",
  "address": "203.0.113.7:52114",
  "awake": false,
  "registeredAt": "2026-10-08T06:00:00Z",
  "lastUpdateAt": "2026-10-08T09:58:12Z",
  "expiresAt": "2026-10-09T09:58:12Z"
}
```

- `identity.mode` is one of `none`, `psk`, `rpk`, `x509`, `oscore`, `broker`; the fields that apply to the mode are set (`pskIdentity`, `certificateSubject`, `publicKey`, `oscoreSenderId`).
- `contentFormats` use the names in §7.3.

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/devices/{ep}/registration` | The current registration; 404 `not_registered` if none |
| `DELETE` | `/v1/devices/{ep}/registration` | Remove it as an operator action: observations end, queued operations for this session are dropped, the transport session is closed where the binding allows it. Event `registration.deleted` with reason `removed`. The device can register again. |
| `GET` | `/v1/devices/{ep}/registrations` | Past registrations (newest first), kept as long as the event retention |

### 5.4 Device model

`GET /v1/devices/{ep}/model` returns the object definitions at the versions this device registered, resolved against the model registry (§15), so a UI can draw the object tree with names, units, types and allowed operations without a second lookup:

```json
{"objects": [{"id": 3, "version": "1.2", "instances": [0], "definition": { "...": "§15 object" }}]}
```

Objects with no known definition have `"definition": null`.

## 6. Operations

### 6.1 Lifecycle

`POST /v1/devices/{ep}/operations` creates an operation. The body is one of the types in §6.3, with these common fields:

| Field | Default | Meaning |
|---|---|---|
| `type` | required | §6.3 |
| `ttlSeconds` | 86400 (tenant setting) | How long the operation may wait before delivery. Past it, it ends as `expired`. 0 means "now or never": an operation that cannot be sent at once ends as `expired` right away. |
| `whenOffline` | `queue` | For a device with no registration: `queue` keeps the operation until the device registers again (within `ttlSeconds`); `reject` fails the call with 409 `not_registered`. |
| `prefix` | none | Gateway end device (§14) |
| `format` | none | Forces the request Content-Format (§7.3). For testing devices; normally omitted. |
| `accept` | none | Forces the response Content-Format |

```
POST /v1/devices/urn:imei:350000000000001/operations?wait=30
{"type": "read", "paths": ["/3/0"]}
```

`?wait=N` (0 to 60 seconds) holds the response until the operation ends or N seconds pass. If it ends in time the reply is **200** with the finished operation; otherwise **202** with its current state and a `Location` header. Without `wait`, the reply is always 202.

```json
{
  "id": "op_01J9Z0...",
  "device": "urn:imei:350000000000001",
  "type": "read",
  "request": {"paths": ["/3/0"]},
  "status": "succeeded",
  "createdAt": "2026-10-08T10:00:00Z",
  "sentAt": "2026-10-08T10:00:00.020Z",
  "completedAt": "2026-10-08T10:00:00.410Z",
  "expiresAt": "2026-10-09T10:00:00Z",
  "createdBy": {"kind": "apiKey", "id": "key_01J..."},
  "job": null,
  "result": {
    "code": "2.05",
    "format": "senml-cbor",
    "values": [
      {"path": "/3/0/0", "type": "string", "value": "Acme"},
      {"path": "/3/0/9", "type": "integer", "value": 87}
    ]
  }
}
```

| Status | Terminal | Meaning |
|---|---|---|
| `queued` | no | Waiting: the device is sleeping or offline, or an earlier operation for it is in flight |
| `sent` | no | On the wire, awaiting the response |
| `succeeded` | yes | The device answered 2.xx |
| `failed` | yes | The device answered 4.xx or 5.xx (`result.code`), or the request could not be encoded (`error.code: "encode"`) |
| `timeout` | yes | No answer within the exchange lifetime, retransmissions included |
| `expired` | yes | `ttlSeconds` ran out before delivery |
| `dropped` | yes | The registration it was queued for ended and `whenOffline` is `reject` (ErrQueueDropped) |
| `cancelled` | yes | Cancelled through the API before it was sent |

Failed and timed-out operations carry `error`:

```json
{"status": "failed", "result": {"code": "4.04"}, "error": {"code": "not_found", "message": "Not Found"}}
```

The `error.code` values for device answers: `bad_request` (4.00), `unauthorized` (4.01), `bad_option` (4.02), `forbidden` (4.03), `not_found` (4.04), `method_not_allowed` (4.05), `not_acceptable` (4.06), `request_entity_incomplete` (4.08), `precondition_failed` (4.12), `request_entity_too_large` (4.13), `unsupported_content_format` (4.15), `internal_server_error` (5.00), `not_implemented` (5.01), `service_unavailable` (5.03). Others: `timeout`, `encode`, `decode` (the answer could not be decoded; `result.payload` carries it base64), `transport`.

### 6.2 Delivery

- **Per-device order.** Operations for one device are sent in creation order, one at a time (CoAP NSTART=1). An operation is `queued` behind the one in flight.
- **Queue mode.** For a sleeping device, operations wait until it wakes (QM-02). If the device has an SMS number and the tenant enables it (§17.3), the server sends a Registration Update Trigger by SMS to wake it (T §6.6).
- **Across registrations.** A queued operation survives a re-registration and is sent in the new session, unless `whenOffline` is `reject`.
- **Cancellation.** `DELETE /v1/operations/{id}` cancels a `queued` operation (204). A `sent` one cannot be recalled: 409 `already_sent`.

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/devices/{ep}/operations` | Create |
| `GET` | `/v1/devices/{ep}/operations` | List for a device. Filters: `status`, `type`, `job`. Newest first. |
| `GET` | `/v1/operations` | List across devices, same filters plus `device`, `selector` |
| `GET` | `/v1/operations/{id}` | Get. Also takes `?wait=N`, to long-poll for the end. |
| `DELETE` | `/v1/operations/{id}` | Cancel a queued operation |

Finished operations are kept for the event retention (§17.3), then deleted.

### 6.3 Operation types

**Device management** (C §6.3)

| `type` | Body | LwM2M |
|---|---|---|
| `read` | `paths` (1 or more) | One path: Read (DM-01). More: Read-Composite (DM-12). |
| `write` | `values`, `mode` (`replace` or `partial`, default `partial`), `path` (optional) | With `path`: Write on that object, instance or resource; `replace` is PUT, `partial` is POST (DM-05). Without `path`: Write-Composite of `values` (DM-13). |
| `execute` | `path`, `arguments` (optional string, e.g. `"0='a',1"`) | Execute (DM-08) |
| `create` | `path` (`/o`), `values`. Instance ID taken from the values' paths, or assigned by the device when absent (DM-09). | Create |
| `delete` | `path` (`/o/i`) | Delete (DM-10) |
| `discover` | `path`, `depth` (optional, 1.2) | Discover (DM-04). The result has `links` (§7.4), no `values`. |
| `write-attributes` | `path`, `attributes` (§8.2). A `null` attribute removes it. | Write-Attributes (ATT-01) |

**Server account and access control** (C §6.2, C §8)

| `type` | Body | LwM2M |
|---|---|---|
| `read-acl` | none | Read /2; the result has `acl` (below) |
| `set-rights` | `aclInstance`, `shortServerId`, `rights` (subset of `read`, `write`, `execute`, `delete`, `create`) | Partial update of /2/x/2 (DM-14) |
| `set-lifetime` | `lifetimeSeconds` | Write /1/x/1 |
| `set-binding` | `binding` | Write /1/x/7 (REG-25) |
| `set-preferred-transport` | `binding` (one letter) | Write /1/x/22 (GEN-14) |
| `set-profile-hash-algorithm` | `suite` | Write /1/x/27 (PROF-10) |
| `advertise-versions` | none | Write the supported LwM2M versions to /1/x/25 (REG-26) |
| `trigger-update` | `binding` (optional) | Execute /1/x/8 (REG-19, GEN-15) |
| `trigger-bootstrap` | none | Execute /1/x/9 (BS-11) |
| `disable` | none | Execute /1/x/4 (REG-24) |
| `sync-clock` | none | Write the server time to /3/0/13 |

**Firmware** (§11 runs whole updates; these are the single steps)

| `type` | Body | LwM2M |
|---|---|---|
| `firmware-reset` | `viaPackage` (bool) | Reset /5 by writing an empty Package URI, or an empty Package with `viaPackage` |

**Escape hatch** (scope `operations:raw`)

| `type` | Body | LwM2M |
|---|---|---|
| `raw` | `method` (`GET`, `PUT`, `POST`, `DELETE`, `FETCH`, `iPATCH`), `path`, `query` (list), `format`, `accept`, `payload` (base64) | One CoAP request on the normal path: queue mode and timeouts apply. The result has `code`, `format` and `payload` (base64). |

`read-acl` result:

```json
{"acl": [{"instance": 0, "object": 3, "objectInstance": 0, "owner": 1,
          "rights": {"1": ["read", "write", "execute"], "0": ["read"]}}]}
```

Reboot and factory reset are `execute` on `/3/0/4` and `/3/0/5`; they get no type of their own.

### 6.4 Results

| Field | Present for |
|---|---|
| `code` | Every answered operation: the CoAP response code as `"2.05"` |
| `format` | Answers with a payload: the Content-Format name (§7.3) |
| `values` | Read and the reads of composite operations (§7) |
| `links` | Discover (§7.4) |
| `location` | Create: the created instance, `"/3303/2"` |
| `acl` | `read-acl` |
| `payload` | `raw`, and any answer the server could not decode (base64) |

A successful `read` or `write` also updates the device state (§8.3).

## 7. Values

### 7.1 Value

A payload is a flat list of values, one per resource or resource instance, the way SenML and the server core represent it:

```json
[
  {"path": "/3/0/0", "type": "string", "value": "Acme"},
  {"path": "/3/0/7/0", "type": "integer", "value": 3800},
  {"path": "/3/0/13", "type": "time", "value": "2026-10-08T10:00:00Z"},
  {"path": "/3303/0/5700", "type": "float", "value": 21.5, "timestamp": "2026-10-08T09:59:00Z"},
  {"path": "/5/0/0", "type": "opaque", "value": "AAECAw=="},
  {"path": "/10/0/1", "type": "objlnk", "value": "3:0"},
  {"path": "/3303/1"}
]
```

| Field | Meaning |
|---|---|
| `path` | `/o/i/r` or `/o/i/r/ri` |
| `type` | §7.2. In requests, optional when the object model knows the resource; required for unknown objects. |
| `value` | JSON form per type |
| `timestamp` | Time of a measurement, when the device sent one (SenML `t`) |
| `prefix` | Gateway end device (§14), when the payload mixes devices |

An entry with a path and no `type` or `value` is an empty object instance (`/o/i`) or an empty multi-instance resource (`/o/i/r`).

### 7.2 Types

| `type` | JSON value | Notes |
|---|---|---|
| `string` | string | |
| `integer` | number, or a decimal string outside ±(2^53−1) | Signed 64-bit. The string form keeps JavaScript clients exact. |
| `unsigned` | number, or a decimal string above 2^53−1 | LwM2M 1.1+ |
| `float` | number | `"NaN"`, `"Infinity"` and `"-Infinity"` as strings |
| `boolean` | boolean | |
| `opaque` | base64 string (RFC 4648 §4, padded) | |
| `time` | RFC 3339 string | Whole seconds on the wire |
| `objlnk` | `"oid:iid"` | `"65535:65535"` is the null link |
| `corelnk` | string, CoRE link-format | LwM2M 1.1+ |

Writes are checked against the device's object model before they are sent (DM-06): an unknown resource, a read-only resource, a wrong type or a value outside the defined range is a 400 on the API call, and nothing goes to the device.

### 7.3 Content-Format names

| Name | Content-Format |
|---|---|
| `text` | 0 |
| `link-format` | 40 |
| `opaque` | 42 |
| `cbor` | 60 |
| `senml-json` | 110 |
| `senml-cbor` | 112 |
| `senml-etch-json` | 320 |
| `senml-etch-cbor` | 322 |
| `tlv` | 11542 |
| `oma-json` | 11543 |
| `lwm2m-cbor` | 11544 |

### 7.4 Links

Discover results and registration object lists as JSON instead of link-format:

```json
[
  {"path": "/3", "attributes": {"ver": "1.2"}},
  {"path": "/3/0", "attributes": {"pmax": 60}},
  {"path": "/3/0/7", "dim": 2}
]
```

## 8. Observations, attributes and state

### 8.1 Observations

An observation is a standing request for notifications (C §6.4).

```
POST /v1/devices/{ep}/observations
{"paths": ["/3303/0/5700"], "attributes": {"pmin": 10, "pmax": 300, "st": 0.5}, "persistent": true}
```

| Field | Default | Meaning |
|---|---|---|
| `paths` | required | One path: Observe. More: Observe-Composite (OBS-05). |
| `attributes` | none | §8.2. For LwM2M 1.2 devices they go in the Observe request (OBS-06); for 1.0 and 1.1 a Write-Attributes on the path comes first. |
| `persistent` | `true` | Re-establish after every new registration and after a server restart. `false` ends the observation with the registration (OBS-03). |
| `prefix`, `format`, `accept` | none | As for operations |

```json
{
  "id": "obs_01J9...",
  "device": "urn:imei:350000000000001",
  "paths": ["/3303/0/5700"],
  "attributes": {"pmin": 10, "pmax": 300, "st": 0.5},
  "persistent": true,
  "status": "active",
  "source": "api",
  "establishedAt": "2026-10-08T10:00:01Z",
  "lastNotificationAt": "2026-10-08T10:05:01Z",
  "notificationCount": 31,
  "error": null
}
```

`status` is `pending` (waiting for the device, as a queued operation would), `active`, `failed` (the device refused; `error` says why; a persistent observation retries at the next registration) or `ended`. `source` is `api` or `profile` (§12); profile observations cannot be deleted one by one.

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/devices/{ep}/observations` | Create. Also takes `?wait=N`. |
| `GET` | `/v1/devices/{ep}/observations` | List for a device |
| `GET` | `/v1/observations/{id}` | Get |
| `PATCH` | `/v1/observations/{id}` | Change `attributes`. The server writes them and re-creates the observation (ATT-12); the ID stays. |
| `DELETE` | `/v1/observations/{id}` | End it. `?mode=active` (default) sends a cancel to the device (OBS-02); `mode=passive` forgets it and resets the next notification. |

Notifications arrive as `notification` events (§16) and update the device state.

### 8.2 Notification attributes

`pmin`, `pmax`, `epmin`, `epmax` (seconds), `gt`, `lt`, `st` (numbers), `edge` (0 or 1), `con` (0 or 1), `hqmax` (integer), with the meaning and validity rules of C §5.1 (ATT-02 to ATT-10). An invalid combination, e.g. `lt` above `gt` with no `st`, is a 400.

### 8.3 Device state

The last known value of every resource the server has seen, from reads, writes, notifications and Sends. A UI shows a device without waking it.

`GET /v1/devices/{ep}/state?paths=/3/0,/3303&prefix=`

```json
{"values": [
  {"path": "/3/0/9", "type": "integer", "value": 87,
   "receivedAt": "2026-10-08T09:58:12Z", "source": "notification"}
]}
```

`source` is `read`, `write`, `notification` or `send`; `timestamp` is set when the device sent one. A successful `delete` drops the instance from the state.

### 8.4 Time series

The history of numeric and boolean resources, from notifications and Sends (and reads, if the profile says so).

`GET /v1/devices/{ep}/timeseries?path=/3303/0/5700&from=...&to=...&interval=300&aggregate=avg`

| Parameter | Meaning |
|---|---|
| `path` | One resource; repeat the parameter for more |
| `from`, `to` | RFC 3339; default the last 24 h |
| `interval`, `aggregate` | Bucket seconds and `avg`, `min`, `max`, `sum`, `count`, `first` or `last`. Without them, raw points, paged. |

```json
{"series": [{"path": "/3303/0/5700", "points": [{"t": "2026-10-08T09:00:00Z", "v": 21.4}]}]}
```

Points use the device timestamp when there is one, else the receive time. Retention is per profile (§12). Time series are an optional capability (`GET /v1/info`); a deployment without a time-series store answers 501 `timeseries_disabled`.

## 9. Credentials and trust

### 9.1 Device credentials

The credential the device uses towards this LwM2M Server (T §5.2).

```
PUT /v1/devices/{ep}/credentials
{"psk": {"identity": "pump-7", "key": "hex:000102030405060708090a0b0c0d0e0f"}}
```

Exactly one of:

| Field | Body | Notes |
|---|---|---|
| `psk` | `identity`, `key`; or `{"generate": true}` | Keys are `hex:` or `base64:` prefixed, at least 16 bytes (BS-10). `generate` creates a random identity and 32-byte key and returns them **once** in the response. |
| `x509` | `{}` | The device's certificate must chain to one of the tenant's trust anchors (§9.3) and carry the endpoint name as CN |
| `rpk` | `publicKey` (base64 SubjectPublicKeyInfo) | 501 `rpk_unsupported` until pion/dtls supports RFC 7250 |

And optionally `oscore`: `masterSecret`, `senderId`, `recipientId`, `masterSalt`, `idContext`, `aeadAlgorithm`, `hmacAlgorithm` (Core E.9), for OSCORE alone or on top of (D)TLS.

| Method | Path | Description |
|---|---|---|
| `PUT` | `/v1/devices/{ep}/credentials` | Set or replace. Replacing closes sessions that used the old credential. |
| `GET` | `/v1/devices/{ep}/credentials` | The credential without secrets: `{"psk": {"identity": "pump-7"}, "oscore": {"senderId": "...", "recipientId": "..."}, "updatedAt": "..."}` |
| `DELETE` | `/v1/devices/{ep}/credentials` | Revoke: the credential, the registration and the session go together, so a compromised key cannot keep a session alive. Event `credentials.revoked`. |

A PSK identity is unique within the deployment: a duplicate is 409 `duplicate_psk_identity`.

### 9.2 Server identity

`GET /v1/info` returns the server's certificate chain (PEM), which a bootstrap config or factory provisioning puts into /0/x/4 for X.509 devices.

### 9.3 Trust anchors

CA certificates whose device certificates the tenant accepts.

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/trust-anchors` | `{"name": "factory CA 2026", "certificates": "-----BEGIN CERTIFICATE-----..."}` |
| `GET` | `/v1/trust-anchors`, `/v1/trust-anchors/{id}` | List, get (subject, issuer, validity, fingerprint) |
| `DELETE` | `/v1/trust-anchors/{id}` | Remove. Existing sessions stay until they end. |

### 9.4 EST

EST over CoAPs (RFC 9148): devices enrol for certificates issued by a tenant CA.

`PUT /v1/est`: `{"enabled": true, "issuer": {"certificate": "PEM", "privateKey": "PEM"}, "validityDays": 365, "selector": "env=prod"}`. Only devices matching `selector` may enrol. `GET` returns it without the key. The issuer becomes a trust anchor of the tenant automatically.

## 10. Bootstrap

### 10.1 Bootstrap config

What the Bootstrap-Server writes on a device (C §6.1). The API pairs each Security instance (/0) with its Server instance (/1) by short server ID, so callers never match instance IDs by hand.

```
PUT /v1/devices/{ep}/bootstrap
{
  "servers": [
    {
      "server": "self",
      "shortServerId": 1,
      "security": {"psk": {"generate": true}},
      "lifetimeSeconds": 86400,
      "binding": "UQ",
      "defaultMinPeriod": 10,
      "defaultMaxPeriod": 3600,
      "disableTimeoutSeconds": 86400,
      "notifyWhenDisabled": false,
      "bootstrapOnRegistrationFailure": true
    },
    {
      "uri": "coaps://other.example.com:5684",
      "shortServerId": 2,
      "security": {"x509": {"certificate": "PEM", "privateKey": "PEM", "serverCertificate": "PEM"}, "sni": "other.example.com"}
    }
  ],
  "bootstrapServer": {"accountTimeoutSeconds": 0, "clientHoldOffSeconds": 0},
  "acls": [{"object": 3, "instance": 0, "owner": 1, "rights": {"1": ["read", "write", "execute"]}}],
  "delete": ["/0", "/1"],
  "writes": [{"path": "/23/0", "values": [{"path": "/23/0/0", "type": "string", "value": "..."}]}],
  "options": {"format": "senml-cbor", "renumberSecurityInstances": true, "discover": false, "read": ["/1"], "refusePack": false}
}
```

| Field | Meaning |
|---|---|
| `servers[].server` | `"self"`: this deployment's LwM2M Server. The server fills in its own URI for the transport in `security`, and with `generate` it also sets the device credentials of §9.1, so one call provisions both sides. |
| `servers[].uri` | Any other LwM2M Server |
| `servers[].security` | `psk`, `x509` (with `certificate`, `privateKey`, `serverCertificate`), `rpk` (501 for now), `none`, and optionally `oscore` (§9.1). Writes /0/x/2 to /0/x/5 and /21. |
| `servers[].securityInstance`, `serverInstance` | Optional fixed instance IDs; assigned otherwise |
| `bootstrapServer` | Changes to the device's own Bootstrap-Server account: /0/x/11 and /0/x/12 (BS-19) |
| `acls` | /2 instances. `instance: 65535` grants Create rights on the object (BS-25). |
| `delete` | Paths deleted before the writes. Default `["/0", "/1"]` (the BS account is kept, BS-04). |
| `writes` | Raw writes after the typed ones, for objects without typed fields (/23, /24, vendor objects; BS-22) |
| `options.format` | Forces the Bootstrap-Write format; otherwise the device's preferred format, else TLV (BS-02) |
| `options.renumberSecurityInstances` | Bootstrap-Discover first and renumber /0 instances around the BS account |
| `options.discover`, `options.read` | Bootstrap-Discover / and Bootstrap-Read of /1, /1/i, /2, /2/i after the writes (BS-05, BS-06) |
| `options.refusePack` | Answer Bootstrap-Pack-Request with 4.05 so the device falls back to Bootstrap-Request (BS-12) |

The config is validated on `PUT` (422 with field errors): short server IDs and instance IDs are unique, ACL owners refer to a provisioned server (BS-26), keys are long enough (BS-10).

| Method | Path | Description |
|---|---|---|
| `PUT` | `/v1/devices/{ep}/bootstrap` | Set or replace |
| `GET` | `/v1/devices/{ep}/bootstrap` | Get, without secrets |
| `DELETE` | `/v1/devices/{ep}/bootstrap` | Remove; the device's next Bootstrap-Request gets 4.03 unless its profile has a template (§12) |

### 10.2 Bootstrap credentials

`PUT/GET/DELETE /v1/devices/{ep}/bootstrap/credentials`, same body as §9.1: the credential the device uses towards the Bootstrap-Server. This is usually the factory credential.

### 10.3 Bootstrap sessions

`GET /v1/devices/{ep}/bootstrap/sessions` lists what each bootstrap did, newest first:

```json
{
  "id": "bs_01J9...",
  "startedAt": "2026-10-08T06:00:00Z",
  "endedAt": "2026-10-08T06:00:03Z",
  "kind": "request",
  "identity": {"mode": "psk", "pskIdentity": "factory-7"},
  "format": "senml-cbor",
  "steps": [
    {"method": "DELETE", "path": "/0", "code": "2.02"},
    {"method": "PUT", "path": "/0/1", "format": "senml-cbor", "code": "2.04"},
    {"method": "POST", "path": "/bs", "code": "2.04"}
  ],
  "discover": [{"path": "/0/0", "attributes": {"ssid": 0}}],
  "reads": {"/1": [ "...values" ]},
  "status": "succeeded",
  "error": null
}
```

`kind` is `request` (Bootstrap-Request) or `pack` (Bootstrap-Pack-Request). `status` is `succeeded` or `failed`, with `error` (e.g. `finish_rejected` with the 4.06 the device sent). Server-initiated bootstrap is the `trigger-bootstrap` operation (§6.3).

## 11. Firmware

### 11.1 Artifacts

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/firmware` | Create metadata: `name`, `version`, `description`, `labels`. Returns the artifact with `status: "empty"`. |
| `PUT` | `/v1/firmware/{id}/content` | Upload the image, `Content-Type: application/octet-stream`. Optional `Digest: sha-256=...`; a mismatch is 400. Sets `status: "ready"`, `size` and `sha256`. |
| `GET` | `/v1/firmware`, `/v1/firmware/{id}` | List, get |
| `GET` | `/v1/firmware/{id}/content` | Download |
| `PATCH` | `/v1/firmware/{id}` | Change `name`, `description`, `labels` (not `version` or content) |
| `DELETE` | `/v1/firmware/{id}` | 409 `in_use` while an update or job uses it |

```json
{"id": "fw_01J9...", "name": "pump-app", "version": "2.4.1", "size": 412337,
 "sha256": "9f86d0...", "status": "ready", "labels": {"hw": "rev-c"},
 "uris": {"coap": "coap://fw.example.com:5693/fw_01J9...", "coaps": "coaps://...", "https": "https://..."},
 "createdAt": "2026-10-01T12:00:00Z"}
```

`uris` are the pull URIs the server's firmware repository serves the image on (FW-02).

### 11.2 Updates

One update per device at a time (409 `update_in_progress` otherwise).

```
POST /v1/devices/{ep}/firmware-updates
{"firmware": "fw_01J9...", "method": "auto", "severity": "mandatory", "maxDeferSeconds": 86400}
```

| Field | Default | Meaning |
|---|---|---|
| `firmware` or `uri` | required | An artifact, or an external Package URI for pull |
| `method` | `auto` | `push` (Block1 Write of /5/0/0), `pull` (write /5/0/1, FW-02) or `auto` (pull when the device lists a protocol we serve in /5/0/8, else push) |
| `severity` | none | `critical`, `mandatory` or `optional` (/5 v1.1) |
| `maxDeferSeconds` | none | Maximum defer period; 0 forbids deferring (/5 v1.1) |
| `autoUpgrade` | `false` | The device installs on download; the server does not Execute /5/0/2 (/5 v1.2) |
| `resume` | `false` | Install a package that is already downloaded, e.g. after a deferral |
| `pushTimeoutSeconds`, `pushAttempts` | 300, 1 | One Block1 transfer's limit and how often a timed-out transfer restarts |
| `pollSeconds` | 30 | Re-read State and Update Result at this interval, for devices whose notifications get lost |

```json
{
  "id": "fwu_01J9...",
  "device": "urn:imei:350000000000001",
  "firmware": "fw_01J9...",
  "method": "pull",
  "uri": "coaps://fw.example.com:5694/fw_01J9...",
  "status": "downloading",
  "deviceState": "downloading",
  "updateResult": {"code": 0, "name": "initial"},
  "progress": {"bytesSent": 131072, "totalBytes": 412337},
  "createdAt": "2026-10-08T10:00:00Z",
  "completedAt": null,
  "outcome": null
}
```

- `status`: `pending` (waiting for the device), `downloading`, `downloaded`, `updating`, `succeeded`, `failed`, `deferred` or `cancelled`.
- `deviceState` is /5/0/3 and `updateResult` is /5/0/5 (FW-04), both by name.
- `progress` is only set for push.
- `outcome`, when finished: `{"objectVersion": "1.1", "added": ["/33000/0"], "removed": []}`. A device whose object set changed re-registers (FW-05).

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/devices/{ep}/firmware-updates` | Start |
| `GET` | `/v1/devices/{ep}/firmware-updates` | History for the device, newest first |
| `GET` | `/v1/firmware-updates/{id}` | Get |
| `DELETE` | `/v1/firmware-updates/{id}` | Cancel: stops the transfer and resets /5 |

## 12. Device profiles

A profile is what devices of one kind have in common. It is applied on every registration of a device that uses it, idempotently.

```json
{
  "id": "dp_01J9...",
  "name": "Pump controller rev C",
  "observations": [{"paths": ["/3303/0/5700"], "attributes": {"pmin": 60, "pmax": 900}}],
  "attributes": [{"path": "/3/0/9", "attributes": {"lt": 20}}],
  "onRegister": [{"type": "sync-clock"}],
  "timeseries": {"retentionDays": 90, "fromReads": false},
  "bootstrapTemplate": { "...": "a §10.1 config, used for devices that have none" }
}
```

| Field | Meaning |
|---|---|
| `observations` | Observations (`source: "profile"`) established after every registration |
| `attributes` | Write-Attributes sent after every registration |
| `onRegister` | Operations (§6.3 bodies) queued after every registration |
| `timeseries` | Retention, and whether reads also feed the time series |
| `bootstrapTemplate` | The bootstrap config for devices of this profile without their own, with `"server": "self"` and `generate`, so devices of the same kind need no per-device bootstrap setup |

`GET/POST /v1/device-profiles`, `GET/PATCH/DELETE /v1/device-profiles/{id}`. Changing a profile applies to devices at their next registration; `POST /v1/device-profiles/{id}/apply` applies it to every registered device now (as a job, §13). Deleting a profile in use is 409.

## 13. Fleet jobs

A job runs one action on many devices, in batches, with a stop rule.

```
POST /v1/jobs
{
  "name": "pump-app 2.4.1 to prod",
  "target": {"selector": "env=prod,hw=rev-c"},
  "action": {"firmwareUpdate": {"firmware": "fw_01J9...", "method": "auto"}},
  "rollout": {"maxConcurrent": 50, "failureThresholdPercent": 5, "startAt": "2026-10-09T01:00:00Z"},
  "ttlSeconds": 604800
}
```

| Field | Meaning |
|---|---|
| `target` | `selector` or `devices` (a list of endpoints). Resolved once, when the job starts. |
| `action` | Exactly one of `operation` (a §6.3 body), `observation` (a §8.1 body) or `firmwareUpdate` (a §11.2 body) |
| `rollout.maxConcurrent` | Devices worked on at the same time |
| `rollout.failureThresholdPercent` | The job pauses itself when this share of finished tasks has failed (after at least 10 tasks) |
| `rollout.startAt` | Scheduled start; default now |
| `ttlSeconds` | Tasks not done by then end as `expired` |

```json
{"id": "job_01J9...", "name": "...", "status": "running",
 "counts": {"total": 1200, "pending": 950, "running": 50, "succeeded": 197, "failed": 3, "expired": 0, "cancelled": 0},
 "createdAt": "...", "startedAt": "...", "completedAt": null}
```

`status`: `scheduled`, `running`, `paused`, `succeeded` (no task failed), `completed_with_failures`, `cancelled`.

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/jobs` | Create |
| `GET` | `/v1/jobs`, `/v1/jobs/{id}` | List, get |
| `GET` | `/v1/jobs/{id}/tasks` | Per-device tasks: `device`, `status`, and the `operation`, `observation` or `firmwareUpdate` ID. Filter by `status`. |
| `POST` | `/v1/jobs/{id}/pause`, `/resume`, `/cancel` | Cancel stops new tasks and cancels queued operations; running firmware updates finish. |

## 14. Gateway end devices

A LwM2M Gateway (/25, /26) carries end devices that are addressed by a prefix (GW §8.3.1).

`GET /v1/devices/{ep}/end-devices` lists them, from the gateway's registration and /25:

```json
{"items": [{"prefix": "d01", "id": "sensor-0042", "instance": 0,
            "objects": [{"id": 3303, "version": "1.1", "instances": [0]}]}]}
```

Operations, observations and state take `prefix` to address an end device instead of the gateway. Events about an end device carry `prefix`.

## 15. Object models

The registry of object definitions: the OMA objects 0–28 built in, the OMA public registry, and vendor objects a tenant uploads.

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/models` | List: `{"id": 3303, "name": "Temperature", "urn": "urn:oma:lwm2m:ext:3303", "versions": ["1.0", "1.1"], "class": "oma-ext", "source": "builtin"}`. Filters: `q`, `class`. |
| `GET` | `/v1/models/{id}` | One object, `?version=` (default the latest) |
| `POST` | `/v1/models` | Upload an OMA object XML (`Content-Type: application/xml`); tenant-scoped. 400 with the parse error if invalid. |
| `DELETE` | `/v1/models/{id}/versions/{version}` | Remove an uploaded definition (built-in ones cannot be removed) |

```json
{
  "id": 3303, "version": "1.1", "name": "Temperature", "urn": "urn:oma:lwm2m:ext:3303:1.1",
  "lwm2mVersion": "1.0", "multiple": true, "mandatory": false, "description": "...",
  "resources": [
    {"id": 5700, "name": "Sensor Value", "operations": "R", "type": "float",
     "multiple": false, "mandatory": true, "units": "Cel", "range": "", "description": "..."}
  ]
}
```

`operations` is a combination of `R`, `W` and `E`, as in the OMA XML; `type` uses the names of §7.2, or `none` for executable resources.

## 16. Events

### 16.1 Envelope

Events are CloudEvents 1.0 in JSON structured mode:

```json
{
  "specversion": "1.0",
  "id": "evt_01J9Z0...",
  "source": "/tenants/acme",
  "type": "lwm2m.notification",
  "time": "2026-10-08T10:05:01.120Z",
  "subject": "urn:imei:350000000000001",
  "sequence": "000000000001843921",
  "datacontenttype": "application/json",
  "data": {
    "observation": "obs_01J9...",
    "prefix": null,
    "values": [{"path": "/3303/0/5700", "type": "float", "value": 21.7}]
  }
}
```

`sequence` increases within a tenant and is what replay resumes from. Delivery is at least once: consumers drop duplicates by `id`. Events for one device arrive in order.

### 16.2 Types

| `type` | `data` |
|---|---|
| `lwm2m.device.created`, `.updated`, `.deleted` | `device` (§5.1) |
| `lwm2m.registration.created` | `registration`, `replaced` (the registration this Register replaced, or null) |
| `lwm2m.registration.updated` | `registration`, `changed` (the fields that changed, e.g. `["objects", "address"]`) |
| `lwm2m.registration.deleted` | `registration`, `reason`: `deregistered`, `expired`, `replaced` or `removed` |
| `lwm2m.device.awake`, `lwm2m.device.asleep` | `registrationId` (queue mode, QM-02, QM-03) |
| `lwm2m.notification` | `observation`, `prefix`, `values` |
| `lwm2m.send` | `values`, `format` (C §6.4.6) |
| `lwm2m.observation.updated` | `observation` (§8.1), on every status change |
| `lwm2m.operation.updated` | `operation` (§6.1), on every status change; terminal statuses included |
| `lwm2m.bootstrap.completed` | `session` (§10.3) |
| `lwm2m.firmware-update.updated` | `firmwareUpdate` (§11.2), on every status or device-state change |
| `lwm2m.job.updated` | `job` (§13), on status changes and at most every 10 s for counts |
| `lwm2m.credentials.revoked` | `endpoint` |

### 16.3 Server-Sent Events

`GET /v1/events` streams events as `text/event-stream`; each event's SSE `id` is its `sequence`.

| Parameter | Meaning |
|---|---|
| `type` | Comma-separated types; a trailing `*` matches a prefix (`lwm2m.registration.*`) |
| `device`, `selector` | Restrict to devices |
| `since` | A `sequence` to resume after; the `Last-Event-ID` header does the same |

A resume from a sequence older than the retention gets an `event: reset` first. The stream sends a comment every 15 s to keep proxies open.

`GET /v1/devices/{ep}/events` is the same history as a paged list (newest first, filter `type`), for activity views.

Events are kept for the tenant's retention (§17.3, default 7 days).

### 16.4 Webhooks

```
POST /v1/webhooks
{"url": "https://example.com/hooks/lwm2m", "types": ["lwm2m.notification", "lwm2m.registration.*"], "selector": "env=prod"}
```

The response includes `secret` once. Each delivery is a `POST` of one event, signed per the Standard Webhooks spec: headers `webhook-id` (the event ID), `webhook-timestamp`, `webhook-signature: v1,<base64 HMAC-SHA256 of id.timestamp.body>`.

- Any 2xx is success. Anything else, or no answer in 10 s, is retried with exponential backoff for 24 h.
- A webhook that has failed for 3 days is disabled (`status: "disabled"`, event `lwm2m.webhook.disabled`).
- Deliveries for one device are made in order: a failing event holds back later events for that device on that webhook.

| Method | Path | Description |
|---|---|---|
| `POST`, `GET` | `/v1/webhooks` | Create, list |
| `GET`, `PATCH`, `DELETE` | `/v1/webhooks/{id}` | Get, change (`url`, `types`, `selector`, `status`), delete |
| `POST` | `/v1/webhooks/{id}/rotate-secret` | New secret; the old one stays valid for 24 h |
| `POST` | `/v1/webhooks/{id}/test` | Send a `lwm2m.webhook.test` event |
| `GET` | `/v1/webhooks/{id}/deliveries` | Attempts: event, time, HTTP status, latency, error |
| `POST` | `/v1/webhooks/{id}/deliveries/{deliveryId}/retry` | Re-send one |

A message bus sink (MQTT, NATS, Kafka) for the same events is deployment configuration, not API.

## 17. Tenants, access and settings

### 17.1 Tenants

`platform` scope only. A single-tenant install has `default` and needs none of this.

| Method | Path | Description |
|---|---|---|
| `POST`, `GET` | `/v1/tenants` | Create (`id`, `name`, `limits`), list |
| `GET`, `PATCH`, `DELETE` | `/v1/tenants/{id}` | `status: "suspended"` refuses the tenant's API calls and device registrations; `DELETE` removes all of its data |

`limits`: `devices`, `operationsPerMinute`, `eventsPerSecond`, `firmwareStorageBytes`, `timeseriesRetentionDays`. Going over a limit is 429 on the API and a refused registration on the device side.

`GET /v1/tenants/{id}/usage`: device count, registered count, operations, events and storage per day.

### 17.2 API keys

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/api-keys` | `{"name": "erp sync", "scopes": ["devices:read"], "expiresAt": "..."}`; the response has `token` once |
| `GET` | `/v1/api-keys` | List: `id`, `name`, `scopes`, `createdAt`, `expiresAt`, `lastUsedAt` |
| `DELETE` | `/v1/api-keys/{id}` | Revoke at once |

### 17.3 Settings

`GET/PATCH /v1/settings`, per tenant:

```json
{
  "registration": {"autoCreateDevices": false, "allowNoSec": false, "defaultProfile": null},
  "queueMode": {"smsWakeUp": false},
  "operations": {"defaultTtlSeconds": 86400},
  "events": {"retentionDays": 7},
  "registrationProfiles": {"oma:1": [{"id": 3, "version": "1.2"}, {"id": 3303}]}
}
```

| Setting | Meaning |
|---|---|
| `registration.autoCreateDevices` | Create a device for an unknown endpoint that authenticates through a trust anchor, or over NoSec when allowed (§5.2) |
| `registration.allowNoSec` | Accept NoSec registrations at all (REG-06). Only one tenant of a deployment can take unknown NoSec endpoints. |
| `queueMode.smsWakeUp` | Wake sleeping devices by SMS when operations queue (T §6.6) |
| `registrationProfiles` | Pre-configured LwM2M Profile IDs and their object lists (PROF-04). Not to be confused with device profiles (§12). |

### 17.4 Audit log

`GET /v1/audit-log` (scope `admin`): every API call that changes something.

```json
{"id": "aud_...", "time": "...", "actor": {"kind": "user", "id": "user_123", "name": "ana@example.com"},
 "action": "operation.create", "resource": "/v1/devices/urn:imei:.../operations/op_...",
 "status": 202, "ip": "198.51.100.4", "summary": {"type": "execute", "path": "/3/0/4"}}
```

Secrets never appear in `summary`. Filters: `actor`, `action`, `resource`, `from`, `to`.

## 18. Server

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/v1/info` | any token | Version, LwM2M versions served, capabilities, device-facing URIs, server certificate |
| `GET` | `/v1/openapi.json` | none | The OpenAPI 3.1 document |
| `GET` | `/healthz` | none | The process is alive |
| `GET` | `/readyz` | none | Listeners are up and the stores are reachable |
| `GET` | `/metrics` | configurable | Prometheus: registrations, handshakes, retransmissions, queue depth, operation latency, webhook backlog |

```json
{
  "version": "0.4.0",
  "lwm2mVersions": ["1.0", "1.1", "1.2"],
  "capabilities": {"bootstrap": true, "firmware": true, "timeseries": true, "est": false, "oscore": true},
  "endpoints": {
    "server": {"coap": "coap://lwm2m.example.com:5683", "coaps": "coaps://lwm2m.example.com:5684", "coaps+tcp": "coaps+tcp://lwm2m.example.com:5684"},
    "bootstrap": {"coaps": "coaps://bs.example.com:5784"},
    "firmware": {"coaps": "coaps://fw.example.com:5694"}
  },
  "serverCertificate": "-----BEGIN CERTIFICATE-----..."
}
```

## Appendix A. Coverage of the Go API

Every exported operation of the server packages, and where the API exposes it.

| Go | API |
|---|---|
| `server.Read`, `ReadComposite` | `read` operation |
| `server.Write`, `WriteComposite` | `write` operation |
| `server.Execute` | `execute` operation |
| `server.Create`, `Delete`, `Discover` | `create`, `delete`, `discover` operations |
| `server.WriteAttributes` | `write-attributes` operation |
| `server.Observe`, `ObserveComposite` | `POST /devices/{ep}/observations` |
| `server.ChangeObservationAttributes` | `PATCH /observations/{id}` |
| `server.CancelObservation` | `DELETE /observations/{id}` |
| `server.Observations` | `GET /devices/{ep}/observations` |
| `server.ReadACL`, `SetRights` | `read-acl`, `set-rights` operations |
| `server.SetLifetime`, `SetBinding`, `SetPreferredTransport`, `SetProfileHashAlgorithm` | `set-*` operations |
| `server.AdvertiseVersions`, `SyncClock`, `TriggerUpdate`, `TriggerBootstrap`, `Disable` | Operations of the same names |
| `server.Do` | `raw` operation |
| `server.Store` (`ByEndpoint`, `All`) | Devices and registrations |
| `server.RemoveRegistration` | `DELETE /devices/{ep}/registration` |
| `server.Security`, `RevokeSecurity` | `/devices/{ep}/credentials` |
| `server.Awake` | `registration.awake`, device status `sleeping` |
| `Config.OnEvent` (all event types) | Events (§16) |
| `Config.OnQueued` | `settings.queueMode.smsWakeUp` |
| `Config.Authorize` | Credentials, trust anchors, `settings.registration` |
| `Config.Profiles` | `settings.registrationProfiles` |
| `transport/coap` `OSCORE.Put` | `credentials.oscore` |
| `bootstrap.ConfigStore` | `/devices/{ep}/bootstrap` |
| `bootstrap.Server.Security`, `OSCORE.Put` | `/devices/{ep}/bootstrap/credentials` |
| `bootstrap.Config.OnSession` | `/devices/{ep}/bootstrap/sessions`, `lwm2m.bootstrap.completed` |
| `bootstrap.Server.MountEST` | `/est` |
| `fota.Manager.Run`, `Cancel` | `/devices/{ep}/firmware-updates` |
| `fota.Manager.Reset` | `firmware-reset` operation |
| `fota.Manager.Protocols` | Read `/5/0/8`; used by `method: auto` |
| `fota.FileServer.Add`, `Remove` | `/firmware` artifacts |
| `gateway.Client` (all methods), `ParseDevices` | `prefix` on operations, `/devices/{ep}/end-devices` |
| `model.Registry.Register`, `Get`, `Versions` | `/models` |

Not exposed, because they are for transport bindings or the process: `HandleUplink`, `Registered`, `KnownObservation`, `Wake`, `Close`, `Config`, the `Listen*` methods.

## Appendix B. What the server needs before v1

The API presupposes parts of ROADMAP.md §4 that do not exist yet. In build order:

1. **Persistence.** A storage layer with one durable implementation (PostgreSQL, with SQLite for single-node installs) for devices, credentials, bootstrap configs, operations, observations, events, firmware metadata and settings. Firmware images go to a blob store (filesystem or S3).
2. **Device registry.** Devices that exist without a registration, labels, the tenant on every record.
3. **Authentication and scopes.** API keys and JWT validation, tenant scoping on every query.
4. **Operation queue.** Durable and per device, surviving re-registrations and restarts. Today a request queued for a sleeping client is dropped when its registration ends (`ErrQueueDropped`).
5. **Event log.** Sequenced and retained, feeding SSE and the webhook dispatcher.
6. **Persistent observations.** Re-established on every registration.
7. **Server API gaps from ROADMAP.md §3.** `Prefix` on every gateway operation (only Read and Observe take it today), and Create without an instance ID (DM-09).
8. **Identity modes** `oscore` and `broker` in `server.Identity` (ROADMAP.md §3).
9. **State and time series stores**, device profiles, jobs, audit log.

Items 1 to 6, with devices, operations, observations, credentials and SSE, are the smallest API a console can run on. Bootstrap, firmware, webhooks, profiles, jobs and time series follow.

## Appendix C. Open questions

1. **Endpoint names unique per deployment.** This design assumes it, since the endpoint name is the registration key on the CoAP side. Per-tenant uniqueness would need a tenant-specific device-facing URI (hostname or SNI per tenant).
2. **Time series in the server.** This design puts them in `lwm2md`, with a pluggable store, so self-hosted users get history too. The alternative is to keep `lwm2md` stateless about telemetry and let consumers store events.
3. **Large integers.** Numbers within ±(2^53−1), decimal strings outside. The alternative is always strings for `integer` and `unsigned`: exact everywhere, but awkward for the common case.
4. **Gateway end devices as devices.** Here they are addressed by `prefix` on their gateway. Making them first-class devices (own labels, profiles, state) is cleaner for fleets of sensors behind gateways, at the cost of IDs like `{gateway}/{prefix}`.
