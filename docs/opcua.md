# OPC UA engine

Implemented with the MIT-licensed `github.com/gopcua/opcua` v0.9.1 client. The implementation is original application code using public client APIs. It does not embed a server or require an external runtime in distributed binaries. The Go server package is used only by disposable loopback tests.

## Safety defaults

- Exact endpoint only: `opc.tcp://host:port/path`. No address scanning, implicit endpoint hopping, or credentials in the URL.
- Default security is `Basic256Sha256` / `SignAndEncrypt`; no automatic downgrade and no trust-on-first-use.
- Secure sessions require a known server leaf certificate SHA-256 pin or an explicit PEM CA trust file. If both are supplied, both must pass. Also checks certificate validity, hostname/IP SAN, and the advertised application URI against the certificate URI SAN.
- Client RSA certificate/key are required for secure channels. Username and user-certificate identities require `SignAndEncrypt`.
- `None` policy and mode require explicit `allow_insecure: true` and anonymous authentication; suitable for a deliberately selected local simulator only.
- Discovery returns **untrusted metadata**. Obtain and verify certificate fingerprints through an independent trusted channel; copying a freshly discovered fingerprint without verification is not secure provisioning.
- Write and method-call actions pass the shared explicit write confirmation gate. A method may modify equipment even if its name sounds read-only. Never test these examples against live industrial equipment.
- Reconnect is disabled. The engine does not transparently replay a failed write or method call. A lost response means the result may be unknown; inspect the target before deciding to retry.

## Secure profile example

No keys or passwords belong in a checked-in collection. Paths point to existing local files and the pin comes from your trusted certificate inventory. Environment references are resolved by the shared profile layer.

```yaml
version: 1
profiles:
  approved-lab:
    endpoint: opc.tcp://lab.example.test:4840
requests:
  - id: read-temperature
    protocol: opcua
    action: read
    endpoint: ${endpoint}
    timeout: 20s
    params:
      node_ids: ["ns=2;s=Temperature"]
      security_policy: Basic256Sha256
      security_mode: SignAndEncrypt
      cert_file: ${env:OPCUA_CLIENT_CERT_FILE}
      key_file: ${env:OPCUA_CLIENT_KEY_FILE}
      server_cert_sha256: ${env:OPCUA_SERVER_CERT_SHA256}
      auth: anonymous
```

For CA trust, use `ca_file` (PEM roots, with server-supplied intermediates) instead of, or in addition to, the pin. There is no default system-root or trust-all fallback. `server_cert_sha256` is exactly 64 hexadecimal digits; colon separators are accepted. The hash is of the leaf certificate DER, not the PEM file or entire concatenated chain.

For username identity add `auth: username`, `username: ${env:OPCUA_USERNAME}`, and `password: ${env:OPCUA_PASSWORD}`. For a distinct X509 user identity use `auth: certificate`, `auth_cert_file`, and `auth_key_file`. These are separate from the secure-channel `cert_file`/`key_file`. Private keys must be PEM RSA key pairs supported by Go's X509 loader; encrypted PEM keys are not supported.

## Actions and parameters

Common: `security_policy` (`Basic256Sha256`, or explicitly allowed `None`), `security_mode` (`SignAndEncrypt`, `Sign`, `None`), `allow_insecure` Boolean, `auth` (`anonymous`, `username`, `certificate`), certificate/trust/authentication parameters above. Parent request `timeout` bounds the operation. Each protocol request and connection is additionally bounded; cleanup uses its own short deadline after cancellation.

| Action | Parameters and behavior |
|---|---|
| `discover` | No authenticated session; emits advertised endpoint URL, policy, mode, application URI and leaf fingerprint with `trusted: false` |
| `browse` | `node_id` defaults to `i=85` (Objects). Follows forward references and BrowseNext continuation pages. `max_references` defaults to 1000, range 1–100000. Reaching the bound is an explicit incomplete-result error and releases the continuation point |
| `read` | `node_id`, or `node_ids` list (maximum 256). Reads Value with source/server timestamps and status. A non-Good per-node status is reported and fails the operation |
| `write` | `node_id`, required `value_type`, `value`. One typed Value write; requires confirmation. Per-node server status is checked |
| `call` | `object_id`, `method_id`, `arguments` list of `{type: Int32, value: 6}` (maximum 64). Reports typed output values and input status codes. Requires confirmation; checks method and argument status |
| `subscribe` | `node_id` or `node_ids`; `interval_ms` default 1000, range 50–60000; `max_events` default 10, range 1–100000. Real monitored Value data-change notifications with statuses/timestamps. Stops at the count, cancellation or deadline; deletes subscription and closes session |

Node identifiers use standard forms such as `i=2258`, `ns=2;s=Temperature`, or standard GUID/byte-string NodeIds accepted by the library.

`value_type`/argument `type` supports `Boolean`, `SByte`, `Byte`, `Int16`, `UInt16`, `Int32`, `UInt32`, `Int64`, `UInt64`, `Float`, `Double`, `String`, `DateTime` (RFC3339 with timezone), and `ByteString` (base64). Add `[]` for a typed one-dimensional array, maximum 10000 elements. Integer widths/ranges are enforced; fractional integers, missing type/value and non-finite floating values are rejected. Use quoted decimal strings for large 64-bit numbers when interoperating with JSON tools that lose integer precision.

## Verification scope and limits

Unit tests cover typed values/ranges, invalid arguments, certificate pin/CA/hostname/URI/expiry rejection, no insecure downgrade or credentials, continuation release, and the shared write gate. Disposable loopback-server integration exercises discovery, a multi-page browse, read, typed write, a test-only registered method, monitored subscription, and a pinned encrypted anonymous session. Exact run results are tracked by CI; tests never contact an industrial endpoint.

Username and X509-user profile construction is implemented; interoperability with each vendor's identity-policy setup needs testing in an authorized lab. This engine does not implement historical reads, events/alarms, complex ExtensionObjects/structures, multidimensional arrays, CRL/OCSP revocation retrieval, PubSub, or a full vendor-specific OPC UA compliance suite. It does not claim that a successful simulator test makes a command safe for production equipment.

Primary API references: [gopcua v0.9.1 source and examples](https://github.com/gopcua/opcua/tree/v0.9.1), [OPC UA service specification](https://reference.opcfoundation.org/Core/Part4/v105/docs/).
