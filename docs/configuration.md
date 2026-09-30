# Configuration reference

A collection contains `version: 1`, optional named `profiles` (string variables),
and `requests`. Each request has unique `id`, optional `name`, `protocol`, `action`,
`endpoint`, optional `timeout` (default 15s, maximum 24h), and protocol `params`.
Unknown top-level/request fields and multiple YAML documents are rejected.

```yaml
version: 1
profiles:
  staging: {base: 'https://api.example.invalid'}
requests:
  - id: health
    protocol: http
    action: GET
    endpoint: ${base}/health
    timeout: 10s
    params:
      bearer: ${env:STAGING_TOKEN}
```

Explicitly pass `--profile staging`. Missing variables fail before connecting.
Profiles do not inherit values and template expansion is not recursive. YAML keys
under `params` are protocol-specific. Unused params do not grant any permissions.

## Common TLS/auth

`ca_file` appends trusted PEM CAs to system roots for HTTP/MQTT/Kafka.
`cert_file` and `key_file` enable mTLS and must be supplied together. There is no
`skip_verify` escape hatch. `username`, `password` are supported where appropriate;
use `${env:SECRET}` values. File paths are relative to the current working directory.
OPC UA has its own stricter [certificate trust reference](opcua.md).

## HTTP

Endpoint: absolute http(s) URL. Actions: GET, HEAD, OPTIONS, POST, PUT, PATCH, DELETE.

Params: `headers` mapping, `body` string or `json` YAML object, `bearer`,
`username`/`password`, TLS fields. JSON sets Content-Type if absent. HTTP 4xx/5xx
emit the response and return failure. Redirects are displayed, never auto-followed.
Response bodies are capped at 4 MiB.

## Kafka

Endpoint: `host:9092,other:9092` (optional kafka:// prefix). Params:
`tls: true`, TLS fields, `sasl: plain|scram-sha-256|scram-sha-512`, username/password.

- `topics`, `brokers`, `groups`: inspect cluster metadata
- `group`: `group` string
- `lag`: optional `groups` string list; otherwise inspect all
- `offsets`: `topic` string; returns end offsets per partition
- `create-topic`: `topic`, `partitions` (default 1), `replication_factor` (default 1)
- `delete-topic`: `topic` (write confirmation required)
- `alter-topic`: `topic`, `configs` mapping, e.g. `{retention.ms: '3600000'}`
- `produce`: `topic`, `key`, `value` string (up to 4 MiB)
- `consume`: `topic`, `offset: earliest|latest`, `limit` (1..100000, default 100),
  optional substring `filter`. JSON values are decoded, other values rendered as
  text. The diagnostic consumer uses direct consumption, does not join a group,
  and never commits offsets. Operation ends at the limit, timeout or cancellation

Schema Registry and Kafka Connect actions use an HTTP(S) endpoint instead:

- `schemas`: list subjects
- `schema`: `subject`, `version` (default latest)
- `register-schema`: `subject`, `json: {schema: '...Avro schema JSON...'}`
- `connectors`: expanded list
- `connector`: `connector` name, status
- `update-connector`: `connector` name and `json` connector config object

These share the HTTP auth/TLS/size behavior. Registry and Connect administration
are not automatically attempted against the Kafka bootstrap address.

## MQTT

Endpoint: mqtt://, mqtts://, tcp://, ssl://, ws:// or wss:// broker URL.
Actions: `publish`, `subscribe`, `read-one`, `clean-retained`.

Params: `topic` or `topics` string list, `qos: 0|1|2` (default 1), `payload`,
`retain` bool, `client_id` (random per operation when omitted), username/password,
TLS fields, `limit` (default 100), `ignore_retained` bool.
Subscriptions support wildcards and multiple topics. `read-one` stops after one
message. `clean-retained` sends an empty retained message to exactly one literal
topic; wildcard deletion is prohibited. Connections are clean-session, bounded,
and close on completion/cancellation. No automatic background reconnect.

## Modbus

Endpoints: `tcp://host:502`, `rtu+tcp://host:port` (RTU ADU with CRC over TCP), or
`rtu:///dev/ttyUSB0` / `rtu://COM3` for serial RTU. Serial requires OS/device access,
not an external runtime. Default serial params: baud 9600, data_bits 8, parity N,
stop_bits 1. Set these explicitly for the target device.

- `read-holding`, `read-input`, `read-coils`, `read-discrete`
- `write-register`: `value` integer 0..65535
- `write-registers`: `values` list of 1..123 integers
- `write-coil`: boolean `value`

Common params: `unit` (1..247; default 1), zero-based `address` (0..65535),
`count` (1..125; default 1), `samples` (default 1), `interval_ms` (>=10; default
1000), `word_order: ABCD|CDAB|BADC|DCBA` for paired-register values. Registers
include u16/i16/hex/binary/ASCII and paired u32/i32/f32 views. Write requests run
once even when `samples` is greater than one. Broadcast and wraparound addressing
are blocked. Transport calls time out within five seconds, bounded also by the
request timeout; serial cancellation completes at the next transport boundary.
