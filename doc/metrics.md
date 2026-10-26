# Health, readiness, and metrics

`lwm2md` exposes exact paths on its main `-http` listener, outside `/v1`
authentication and independent of the Leshan `-prefix`. They are not mounted
on the bootstrap HTTP listener. Empty `-http` disables these endpoints too.
Restrict access at the network/proxy boundary when needed.

- `GET /healthz`: plain-text `ok`, HTTP 200 while the process serves requests.
  Dependency failures do not affect liveness.
- `GET /readyz`: HTTP 503 until startup completes, then 200. Startup means all
  enabled device-facing listeners and both enabled HTTP listeners have bound
  successfully. Readiness is cleared as soon as shutdown cancellation is received,
  before HTTP draining. Disabled listeners do not block readiness.
- `GET /metrics`: enabled by default; use `-metrics=false` to return HTTP 404.
  Health and readiness remain available with metrics disabled.

Known enabled endpoints accept GET only; other methods return 405 with
`Allow: GET` (including HEAD). Unknown paths and disabled metrics return 404.
Probe/error bodies are plain text, not native API problem JSON.

## Readiness dependencies

`internal/obs.Checker` is a seam for future store-connectivity checks. A configured
checker must succeed for every readiness request after startup; failures return
503 without exposing error details. The daemon currently supplies no checker:
readiness does **not** claim external database or downstream-device connectivity.

## Prometheus exposition

The content type is `text/plain; version=0.0.4; charset=utf-8`.
`lwm2m_registrations` is a gauge with HELP and TYPE metadata, computed on every
scrape as `len(srv.Store().All())`. It counts current stored registrations, not
historical registration events or independently verified reachable devices.
Re-registration replaces the registration rather than incrementing the count;
deregistration removes it. Store expiry semantics determine expiry visibility.

No request, latency, error, observation, queue, or transport metrics are emitted:
those instrumentation hooks are not implemented. No synthetic zero-valued
series stand in for unimplemented instrumentation. A handler without a count
callback emits no registration series.
