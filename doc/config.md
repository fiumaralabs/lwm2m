# lwm2md configuration

Pass `-config` a JSON file to configure the daemon:

```sh
lwm2md -config ./lwm2md.json -http :8088
```

For example:

```json
{
  "coap": ":5683",
  "coaps": ":5684",
  "tcp": "",
  "http": ":8080",
  "metrics": true,
  "timeseries": true,
  "store": "memory",
  "prefix": "/api",
  "api-keys": "",
  "bs-coap": ":5783",
  "bs-coaps": ":5784",
  "bs-http": ":8081",
  "cid": 6,
  "fw-dir": "",
  "fw-coap": ":5693",
  "fw-coaps": ":5694",
  "v": false
}
```

Keys match the flag names without the leading `-`. Omitted keys retain their built-in defaults. Explicit command-line flags override file values, even when the flag value equals the default. In the command above, `-http :8088` overrides the file's `http` value. Use JSON strings for addresses and paths, booleans for switches, and an integer for `cid`. Unknown keys, incorrect types, unreadable files, and extra JSON values stop startup with an error. Run `lwm2md -h` for flag descriptions and defaults.

The `api-keys` setting is a path to a separate key file, not the keys themselves. Protect that file appropriately.

## Device-facing server information

`advertise-coap` and `advertise-coaps` optionally set the device-reachable URIs reported in `/v1/info`, for example `coap://devices.example.com:5683` and `coaps://devices.example.com:5684`. They are also available as flags. Choose addresses reachable by your devices, including any externally mapped ports. The daemon never derives advertised URIs from its bind addresses, and omits a URI when its corresponding listener is disabled. With no advertised URIs, `endpoints` is empty.

`server-certificate` optionally names a file containing only the public PEM certificate chain to return as `serverCertificate` in `/v1/info`. Never include a private key. Invalid or mixed files stop startup. This setting only publishes the certificate; it does not configure a DTLS certificate or enable X.509 on the listener. With no file configured, the field is omitted.

## Admission configuration

Admission is off by default. `admission-url` enables construction of the optional client and requires `store` to be `sqlite:<path>`. The URL must be HTTPS with the path `/v1/admission-decisions`. `admission-timeout` defaults to `5s` and must be positive and no more than `30s`. `admission-request-key-ref` and `admission-response-key-ref` must name distinct environment variables containing the respective signing keys; do not put key values in the JSON configuration. These names also work as flags with a leading `-`.

This configuration prepares the client and persistent binding store only. Registration does not consult the client yet, so enabling it does not enforce an admission policy or disconnect existing devices.
