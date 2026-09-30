# Functional integration and scope

The source repositories use different language stacks and licenses. iotools
implements their useful workflows with a single native Go TUI and protocol engine;
it does not copy GPL application code or require any of the original executables.

| Inspiration | License inspected | Implemented workflows | Remaining upstream differences |
|---|---|---|---|
| [Slumber](https://github.com/Live-yum/slumber) | MIT | File-first HTTP requests, environments, in-TUI YAML editing, CLI, auth/TLS, JSON/raw responses | Original Slumber YAML/import formats, request chaining, JSONPath, shell/file templates, fork-specific crypto transformations |
| [ktea](https://github.com/jonas-grgt/ktea) | Apache-2.0 | Topic/broker/group metadata, lag/offsets, topic admin, produce/consume/filter, Schema Registry and Connect REST, TLS and SASL | Rich specialized group/schema tables, Avro wire decoding, Connect lifecycle commands, ACL administration |
| [mqttui](https://github.com/EdJoPaTo/mqttui) | GPL-3.0 license text | Wildcard subscriptions, publish, QoS/retain, read-one, exact-topic retained cleanup, timestamped events | Hierarchical topic-tree widget, recursive retained cleanup, automatic reconnect |
| [MTUI](https://github.com/inowattio/MTUI) | GPL-2.0 license text | TCP/RTU/RTU-over-TCP, periodic register reads, unit selection, coil/register writes, word ordering, numeric views | Pin/label panels, plotted trends, snapshot diff UI, M10K/f64 columns, standalone HTTP server |
| [ua-client](https://github.com/FreeOpcUa/ua-client) | MIT | Discovery, browse continuation, attribute reads, typed writes/method calls, data subscriptions, anonymous/password/X509 auth, verified encrypted endpoints | Interactive lazy address tree, reconnect state restoration, custom structured values, event subscriptions |

The current TUI intentionally shares saved-request/detail/result panels for every
protocol; specialized upstream screens are not yet feature-identical. This matrix
is the acceptance boundary and a backlog, not a claim of complete upstream parity.

## Verification layers

1. Configuration validation, template isolation, atomic-save preservation and write
   gating unit tests
2. Simulated-terminal render/resize/editor invalid-save/cancel/reopen/read-only tests
3. Native loopback HTTP and MQTT servers, Kafka wire-compatible kfake broker,
   Modbus TCP/RTU-over-TCP fixtures, OPC UA server for wire-level behavior
4. Native Windows x64, Linux ARM64 and Linux x64 Actions jobs, race checks on Linux,
   CGO-disabled builds, CLI smoke tests, dependency license packaging

Physical serial-device acceptance, a production Kafka distribution, and external
industrial servers are separate from isolated loopback test evidence. No passing
simulator test should be presented as acceptance on an untested physical device.
