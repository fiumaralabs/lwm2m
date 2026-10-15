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
web API, deregisters and checks it is gone.

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
