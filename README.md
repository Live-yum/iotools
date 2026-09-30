# iotools

A single portable, keyboard-first TUI for HTTP, Kafka, MQTT, Modbus and OPC UA.
Collections are editable YAML files, and the same requests can run in the TUI or
as a JSON-lines CLI. **No Java, Python, Node, Docker, external editor or separately
installed protocol client is required to run the executable.** Remote protocol
servers and a functioning terminal are naturally still required.

## Quick start

Download the matching `iotools-windows-amd64` or `iotools-linux-arm64` artifact
from a successful [Actions run](https://github.com/Live-yum/iotools/actions).
Artifacts also contain license notices, examples and documentation.

```sh
iotools --init
iotools --profile local
# On PowerShell use .\iotools.exe instead of iotools.
```

The example targets localhost only. Nothing connects until you select a request
and press Enter. Press F4 to change endpoints, add requests or edit their params
without leaving the terminal. A server that is not running produces an actionable
connection error; an unavailable server is never silently replaced with mock data.

```sh
iotools --file examples/local.yaml --validate
iotools --file examples/local.yaml --profile local --run http-get
iotools --profile local --run mqtt-publish --allow-writes
iotools --profile production --read-only
```

CLI writes require `--allow-writes`; TUI writes require a per-operation confirmation.
`--read-only` takes precedence. Authentication secrets can be referenced as
`${env:NAME}`; they are resolved only in memory, and never written back to YAML.
All network operations have a timeout; use F8 to cancel streaming operations.
There is no shell-command interpolation, auto-execution, telemetry, credential
creation or background scanning. Results are held in memory, not auto-persisted.
Redirect CLI output to a private file if you want an explicit capture.

## Unified interface

- Left: saved requests, with protocol and action labels
- Right: selected request source; profile variables remain unexpanded
- Bottom: response/event stream with timestamps; bounded to 128 recent events
- Top: request filter; F6 selects an environment profile
- Tab / Shift-Tab changes focus; Enter / F5 runs; F4 edits; F8 cancels; ? opens help
- YAML editor: Ctrl-S validates and atomically saves, Esc discards changes
- Plain terminal fonts are sufficient; no icon font or clipboard helper is needed

## Protocol capabilities

See [the capability matrix](docs/capabilities.md) for the exact implemented scope
and remaining differences from the inspirational tools. This is an original
implementation of their workflows, not a bundled launcher for five programs or a
claim that every upstream feature is already reproduced.

- HTTP: methods, headers, JSON/raw bodies, bearer/basic auth, verified TLS/mTLS,
  response status/headers/body, explicit redirects (not automatically followed)
- Kafka: brokers/topics/partitions/offsets, groups and lag, create/delete/configure
  topics, produce and bounded consume, search, TLS/mTLS and SASL PLAIN/SCRAM,
  Schema Registry and Kafka Connect REST operations
- MQTT: MQTT 3.1.1 publish/subscribe/read-one, multiple wildcard subscriptions,
  retained values, exact-topic retained cleanup, QoS 0/1/2, TLS/mTLS/auth
- Modbus: TCP and serial RTU, unit selection, coils/discrete/input/holding reads,
  register/coil writes, periodic samples, integer/hex/binary/float interpretations
- OPC UA: endpoint discovery, bounded address-space browse with continuation,
  reads, typed writes/method calls, data-change subscriptions, anonymous/password/
  X509 identity, fail-closed encrypted endpoint trust verification

## Build and test

Go is a build-time dependency only. Versions and checksums are pinned in go.mod
and go.sum. The project currently uses Go 1.27.1.

```sh
go test -count=1 -timeout 5m ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o iotools ./cmd/iotools
# Cross compile; CI also executes the suite natively on both targets.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o iotools.exe ./cmd/iotools
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o iotools-arm64 ./cmd/iotools
```

CI runs on native Windows x64, Linux ARM64 and Linux x64. Tests create isolated
loopback protocol servers, exercise actual wire exchanges, verify TUI drawing and
editor behavior, and reject insecure writes and TLS failures. The Kafka fixture is
`kfake`, a protocol-compatible simulator, not a production Kafka distribution.
Physical serial devices and external industrial hardware are never touched.
Test/build artifacts are tied to the exact source SHA. Check the run result rather
than assuming a workflow file alone proves platform support.

## Configuration and security

[Configuration reference](docs/configuration.md) · [Security model](docs/security.md)

The repository is Apache-2.0. Upstream functional inspiration includes
[MTUI](https://github.com/inowattio/MTUI), [ktea](https://github.com/jonas-grgt/ktea),
[ua-client](https://github.com/FreeOpcUa/ua-client),
[mqttui](https://github.com/EdJoPaTo/mqttui) and
[Slumber](https://github.com/Live-yum/slumber). Their source is not incorporated.
MTUI and mqttui have GPL licenses; copying them into an Apache-only combined
binary would not preserve this project's licensing model. Protocol implementations
use separately licensed Go libraries; exact texts are packaged in every artifact.
