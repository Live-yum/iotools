# Security model

- The application never connects just because a collection is opened or selected.
- Write-like operations (including HTTP POST/PUT/PATCH/DELETE, Kafka produce/admin,
  MQTT publish/retained cleanup, Modbus writes and OPC UA method calls) are gated.
  A collection cannot grant itself permission. CLI needs --allow-writes; TUI asks.
- Read-only mode takes precedence over other flags. Modbus broadcast writes are
  disabled; MQTT retained cleanup only accepts one exact topic, never a wildcard.
- TLS certificate verification is never globally disabled. Custom CA bundles and
  client cert/key paths are supported. Credentials are never generated automatically.
- OPC UA defaults to Basic256Sha256 / SignAndEncrypt. Supply a trusted certificate
  SHA-256 pin or CA and the client certificate/key. Endpoint discovery is untrusted
  information, not approval to trust the displayed fingerprint. Validate fingerprints
  with the server administrator over a separate trusted channel. Certificate
  validity, hostname and advertised application URI are checked before credentials.
- Explicit unencrypted OPC UA is for an isolated local simulator only. It requires
  security_policy: None, security_mode: None, allow_insecure: true and anonymous auth.
- YAML templates expand named profile values and explicitly prefixed environment
  variables only. They do not execute commands or implicitly read files.
- Secret environment references remain references when saved. Do not put literal
  secrets into a tracked collection. Request bodies and server results may contain
  private data; do not share screenshots or explicitly exported files indiscriminately.
- Remote terminal control characters are replaced before rendering. TUI markup is
  disabled for response content. Responses, messages, queued updates and retained
  event count are bounded. Errors are shown instead of silently pretending success.
- The binary has no automatic update/download behavior. Build-time dependencies and
  license texts are locked and verified. CI needs network package access, not runtime.

The tool is a diagnostic client, not a production safety controller. Only connect
to systems you are authorized to use. Test writes on an isolated simulator first.
