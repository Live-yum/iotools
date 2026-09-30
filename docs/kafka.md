# Kafka and Avro interoperability

Kafka operations use the native pure-Go franz-go client. No external Kafka,
Java, Docker or command-line helper is required by the shipped binary.

## Registry-backed records

`produce` supports independent `key_format` and `value_format`: `text` (default)
or `avro`. Avro input is **Avro JSON**, including tagged objects for non-null
union values; it is not arbitrary JSON. Each Avro part requires an explicit
`key_subject` or `value_subject`. Select a positive numeric `key_version` or
`value_version`; default `latest` deliberately follows the current registered
version. The client only reads existing schemas and never auto-registers them.
Use the separately write-gated `register-schema` operation to register a schema.

Records use Confluent's magic byte 0 + four-byte big-endian schema ID framing,
including the primitive `bytes` schema's unprefixed payload convention.

`consume` defaults both formats to `auto`: normal JSON/text is readable, and
magic-byte records are decoded with their embedded writer schema ID. Select
`avro` for strict framing or `text` to explicitly disable schema decoding.
Missing registry, unknown schema ID, malformed or trailing payload, non-Avro
schema and external schema references all produce errors; they do not become
corrupted text. Non-UTF-8 text-mode payloads are represented as
`{"encoding":"base64","data":"..."}`. Null Kafka values stay null (tombstones).
Avro longs are rendered without converting them through floating point. The
`filter` parameter is applied to decoded values. This is writer-schema decoding,
not reader-schema evolution. Protobuf, JSON Schema and external Avro schema
references are not currently supported.

## Separate registry connection settings

- `schema_registry_url`: absolute HTTP(S) URL, optional base path; no embedded
  credentials, query or fragment
- `schema_registry_username` and `schema_registry_password`: Basic auth
- `schema_registry_bearer`: bearer token (takes precedence over Basic auth)
- `schema_registry_ca_file`: extra trusted CA bundle
- `schema_registry_cert_file` and `schema_registry_key_file`: optional mTLS pair

Broker `username`, `password`, `sasl`, `tls`, `ca_file`, `cert_file`, `key_file`
remain independent. TLS verification is mandatory; registry redirects are not
followed. Schema responses and input record parts are bounded to 4 MiB. The writer-schema
cache holds at most 128 entries per operation. Avro blocks are limited to
65,536 entries and 4 MiB (oversized blocks fail explicitly). Registry response bodies,
authentication values and raw schema/record content are omitted from codec
errors. Use HTTPS for credential-bearing remote registry connections. Existing
registry administration actions use their HTTP endpoint and ordinary HTTP auth
settings, as documented in the main configuration reference.

All fields use the same collection profile/environment substitution as other
protocols; profile names do not create hidden credential stores. For example:

```yaml
version: 1
profiles:
  local:
    brokers: 127.0.0.1:9092
    registry: http://127.0.0.1:8081
  staging:
    brokers: kafka.example.invalid:9093
    registry: https://registry.example.invalid
requests:
  - id: avro-produce
    protocol: kafka
    action: produce
    endpoint: ${brokers}
    timeout: 15s
    params:
      topic: readings
      value_format: avro
      value_subject: readings-value
      value_version: 2
      value: '{"temperature":21}'
      schema_registry_url: ${registry}
      schema_registry_username: ${env:REGISTRY_USER}
      schema_registry_password: ${env:REGISTRY_PASSWORD}
  - id: avro-consume
    protocol: kafka
    action: consume
    endpoint: ${brokers}
    timeout: 15s
    params:
      topic: readings
      value_format: auto
      limit: 10
      schema_registry_url: ${registry}
      schema_registry_username: ${env:REGISTRY_USER}
      schema_registry_password: ${env:REGISTRY_PASSWORD}
```

The example's staging broker requires the appropriate broker TLS/SASL settings
for that deployment. Never save resolved secrets into a tracked collection.

## Verification and provenance

`go test ./internal/engine -run TestKafka` starts a disposable in-process Kafka
protocol simulator (`kfake`) and HTTP/TLS registry simulators. It covers actual
produce/consume wire traffic, explicit subject/version and ID lookups, Basic
auth, untrusted/trusted TLS, redirects, unsupported schemas, missing/unknown
schema IDs, corrupt records, long precision, key Avro and primitive bytes framing.
No external cluster is contacted. This is protocol-simulator integration, not a
claim of end-to-end verification against every managed Kafka provider.

[ktea](https://github.com/jonas-grgt/ktea) provides a useful capability reference
for native terminal Kafka and Avro/schema-registry workflows. No ktea source was
copied. The codec dependency is
[linkedin/goavro/v2](https://github.com/linkedin/goavro), Apache-2.0 licensed,
pure Go. The implementation uses public Schema Registry HTTP and Confluent wire
format conventions.
