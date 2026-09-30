# Protocol-native TUI workflows

All five protocols keep the same collection/detail/results layout. F2 switches
between structured protocol views and raw timestamped JSON events. Tab focuses
the results pane; mouse selection is also supported.

## MQTT topic tree

Run a subscription to see topic segments grouped hierarchically. Each leaf updates
in place with the latest payload, QoS and retained flag. Enter expands/collapses
branches. Paths are bounded to 1024 tree nodes; raw event output continues even
if new tree paths reach the limit. F8 cancels the subscription. A new run starts a
fresh tree and cannot silently leave an old subscription connected.

## OPC UA node browser

Run a browse request, focus the results, then select a node:

- Enter browses that node's children
- r reads its current value
- s starts a bounded data-change subscription
- Backspace returns to the preceding navigation request
- F8 cancels a running subscription before navigating again

These operations inherit the selected recipe's connection security/auth settings.
They do not modify the saved collection or create credentials. Encrypted endpoints
still require independent certificate trust configuration. Typed writes/method
calls remain explicit saved requests edited using F4, with write confirmation.

## Kafka topic table

Topic listing shows topic names, partition counts, internal-topic status and
per-topic errors. Enter starts a bounded read-only consumer for the selected topic,
using the existing connection/security configuration. No consumer group offset is
committed. Produce/admin requests remain explicit recipes with confirmation.

## Modbus live registers

Periodic register results update rows in place. Columns include integer/hex/float
views, session labels, pin markers, u16 sparklines and delta against a snapshot.

- p toggles the selected register pin
- l opens a label editor; Save/Cancel/Escape are supported
- f toggles pinned-only filtering
- d captures the current values as a fresh snapshot baseline

Pins and labels are session-only and isolated by endpoint/unit. The initial sample
is the default delta baseline; sparklines retain the latest 32 samples. The graph
plots u16 values, not f32 reinterpretations. No network write happens when pinning,
labeling, filtering or taking a snapshot. Actual writes require explicit unit,
address and typed value, plus confirmation.

## Response trees and limits

HTTP JSON and generic results are expandable trees (depth 8 and 300 fields per
event); long scalar previews are truncated. F2 shows raw events for detail. Raw
TUI events retain the latest 128 entries, at most 32 KiB per rendered entry. Use
CLI JSON lines when a full bounded response capture is needed. Terminal escapes
and markup in server-supplied strings are escaped before rendering.
