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
