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

`lwm2m_operations_total{type,status}` counts committed terminal operation
transitions. The labels use the operation types and terminal statuses in
[API.md](API.md) §6.3 and §6.1. Failed conditional transitions do not count.
`lwm2m_webhook_deliveries_total{result}` counts each delivery attempt, including
retries and test deliveries. `result` is `success` for a 2xx response and
`failure` otherwise. Neither metric contains device or tenant labels.

These counters are process-local and reset when the daemon restarts. Only
observed label combinations are emitted; metadata remains visible before the
first event. Scrapes may observe a committed operation immediately before its
counter increment. No other request, latency, observation, queue, or transport
metrics are emitted. A handler without a count callback emits no registration
series.
