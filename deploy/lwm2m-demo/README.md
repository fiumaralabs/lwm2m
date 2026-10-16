# Public demo: lwm2m.fiumaralabs.com

`cmd/lwm2m-demo` is a locked-down `lwm2md` for an open Internet demo: anonymous
CoAP/UDP NoSec on 5683, DTLS-PSK on 5684 (identity = endpoint name, public demo
key `000102030405060708090a0b0c0d0e0f`), and a read-only web page + JSON API on
`127.0.0.1:8090` behind Caddy.

Left out on purpose: the Leshan REST API (no auth; write/execute/delete), the
Bootstrap-Server, the firmware server. Limits: 200 registrations, 10 NoSec
registrations and 30 Register requests/min per source IP, lifetime clamped to
1 h, endpoint names of at most 64 printable ASCII characters, web reads 20/min
per IP and 4 concurrent, never `/0` (Security) or `/21` (OSCORE).

## Web API (read-only)

| Endpoint | Returns |
|---|---|
| `GET /api/clients` | registered clients: version, binding, security, lifetime, `expires_at`, object list with names |
| `GET /api/clients/{ep}` | one client plus its objects with the OMA model (resource names, types, units, operations, enumerations) and the last Send |
| `GET /api/clients/{ep}/read?path=/o[/i[/r]]` | a Read: CoAP code, content format, typed values (`v`), payload hex. Counts against the read rate limit (`X-RateLimit-Remaining`) |
| `GET /api/clients/{ep}/observe?path=/o/i[/r]` | server-sent events: `open`, `value` (same shape as a read), `end` with a reason. One Observe per path shared by all viewers; 2 streams per IP (`-observe-per-ip`), 32 total (`-observe-max`), 4 paths per device, closed after 5 min (`-observe-for`), at most 4 updates/s. Starting one counts as a read |

The page (`web/`, embedded) is plain HTML/CSS/JS with no external requests,
served with `Content-Security-Policy: default-src 'none'; script-src 'self'; ...`.
Object definitions: `model/registry` (core 0-28) plus `cmd/lwm2m-demo/objects`
(IPSO 3200-3350, 3442, 10256 from the OMNA registry). Competitor notes:
`COMPETITORS.md`.

## Build

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o lwm2m-demo ./cmd/lwm2m-demo
```

## Install

Recommended (needs sudo): `lwm2m-demo.service`, a system unit with
`DynamicUser=yes` and full sandboxing. Without sudo:
`lwm2m-demo.user.service` (home hidden with `ProtectHome=tmpfs`, needs
linger).

Then:

1. Open UDP 5683 and 5684 in the host firewall (`sudo ufw allow 5683/udp && sudo ufw allow 5684/udp`) and in the Hetzner Cloud firewall if one is attached.
2. DNS (Cloudflare, **proxied off**: CoAP is UDP):
   `A lwm2m 178.105.125.174`, `AAAA lwm2m 2a01:4f8:c015:e112::1`.
3. Append `Caddyfile.snippet` to `/etc/caddy/Caddyfile`, `sudo systemctl reload caddy`.

## Verify

```sh
go run ./cmd/lwm2m-demo/probe -coap lwm2m.fiumaralabs.com:5683 -web https://lwm2m.fiumaralabs.com
go run ./cmd/lwm2m-demo/probe -coap lwm2m.fiumaralabs.com:5684 -web https://lwm2m.fiumaralabs.com -psk 000102030405060708090a0b0c0d0e0f
```

The probe registers, checks the client is listed, reads `/3/0` through the
web API, deregisters and checks it is gone. `-rich -hold 10m` registers the
simulated sensor that `cmd/lwm2m-client` runs (`internal/simdevice`: Server,
Device, Connectivity, Location, Temperature, Humidity, Voltage, one Send),
whose values change every 5 s, for trying the page.

For a longer-running device, use the demo client:

```sh
go run ./cmd/lwm2m-client -server coap://lwm2m.fiumaralabs.com:5683
go run ./cmd/lwm2m-client -server coaps://lwm2m.fiumaralabs.com:5684 -psk 000102030405060708090a0b0c0d0e0f
```

## Known risks

- No packet-level rate limit: a UDP/DTLS flood is only bounded by the
  registration limits, `MemoryMax` and the kernel. Use the host firewall
  (e.g. nftables `limit rate` on 5683/5684) if it gets abused.
- Per-IP limits apply to NoSec only (the PSK identity hook has no address);
  PSK registrations count against the global cap.
- The server's schema cache is not evicted for ended registrations
  (ROADMAP "Resource bounds"): random endpoint names grow memory slowly.
  `RuntimeMaxSec=1d` restarts daily, which also drops all registrations.
- Endpoint names are shown publicly; the page tells users not to use real IMEIs.
