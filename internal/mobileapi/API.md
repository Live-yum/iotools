# Native Android structured API

`Open(configPath, version string, Options) (*Session, error)` loads an existing app-private collection or creates the bundled local example. It does not open protocol connections. `Options` exposes `read_only`, `history`, and a Go-only `RTUTransport` injection. `Command(string) string`, `Poll() string`, `Pause()`, `Resume()`, and `Close()` are safe bridge entry points. No terminal packages are imported.

Every JSON command has an `op`. Replies are `{ "ok": true, "data": ... }` or `{ "ok": false, "error": "..." }`. Unknown/duplicate fields, trailing documents, excessive nesting, and oversized input fail closed. Inputs allow 8 MiB, source collections 4 MiB, replies 16 MiB. Integers beyond JavaScript's exact 53-bit range are serialized as decimal strings everywhere, including typed protocol event structures. Callers should keep those strings intact when editing typed values.

## Configuration and request forms

- `state` → `{path, version, profile, profiles: [names], requests: [Request], options, running, run_id, paused, closed, revision}`
- `catalog` → `{protocols: [{id,name,actions:[{id,name,mutates,defaults}],fields:[{key,label,type,hint}]}], utilities, limits}`. Form types are `text`, `number`, `boolean`, `json`, `password`; advanced fields preserve the engine's original parameter names
- `config.get` → `{path, source, collection}`. This is an explicit raw editor operation and can contain credentials
- `config.validate {source}` → `{collection}`; no file change
- `config.save {source}` → state. Validates before atomic private replacement and refuses an external change since the loaded source
- `config.reload` → state from disk. Does not execute requests
- `config.import {source,format}` → `{source,collection}` conversion preview only. Formats: `iotools`, `slumber`, `v3`, `rest`, `http`, `vscode`, `jetbrains`, `openapi`, `insomnia`. Commit using `config.save`
- `config.switch {path,confirmed:false}` → `{path,collection,confirmation_required:true,token}`; the same command with `token` and `confirmed:true` loads that private collection and cancels old subscriptions, without network execution
- `request.save {request,original_id?}` → state. Native request replacement preserves unrelated source comments. Slumber source is edited in its source editor or explicitly converted first
- `request.delete {request_id,confirmed:true}` → state
- `profile.set {profile}` → state
- `options.set {options:{read_only,history}}` → state. Go-only USB transport is preserved

A Request uses `{id,name?,protocol,action,endpoint,timeout?,params?}`. Saved config/form retrieval is raw so edits can round-trip. Preview and runtime confirmation views separately mask credentials, crypto definitions, sensitive headers and secret query parameters. The actual request snapshot is never redacted or changed by the presentation view.

## Preview, execution and activity

`preview {request_id}` or `preview {request:Request,profile?,overrides?}` returns `{token,request,mutates,confirmation_required,summary,warnings}`. Preview performs no network activity and does not evaluate HTTP file/response/prompt templates. The nonce binds the request, resolved non-HTTP parameters, collection, profile and revision, expires after five minutes, and is consumed by execution. At most 16 pending previews are retained.

`run {token,confirmed}` returns `{run_id}` and starts asynchronously. Writes require `confirmed:true`; read-only mode is enforced in Go. A write with a changed runtime HTTP target and each mutating workflow dependency or TLS exception receives a separate interactive confirmation. Repeated Run cannot replay a consumed nonce. One foreground operation runs at a time.

`cancel {run_id?}` cancels the foreground operation. Providing `run_id` rejects a stale target. `pause` cancels foreground work, all independent subscriptions and pending interactions, invalidates previews, and closes the platform USB transport. `resume` enables new explicit operations; it never reconnects or replays. The same applies to lifecycle methods.

`events` and `Poll()` return `{events:[{seq,run_id,time,kind,data}],running,run_id,paused,dropped}`. The queue holds at most 256 events / 1 MiB. An individual event above 64 KiB yields a clearly marked `{truncated:true,original_bytes,preview}` view. No credential or result body is silently persisted as event history. Consumers should explain the `dropped` count and retain only bounded display history.

Foreground events start with `started`; protocol-native events follow; `done` contains `{status:"completed|cancelled|failed",error,request_id?}`. Times use RFC3339/RFC3339Nano UTC. HTTP data retains raw-body Base64 and transformed views separately. OPC `reference` data is flattened to `{node_id,display_name,browse_name,namespace,node_class,is_forward}`. MQTT `message` retains topic and payload metadata; Kafka `record` retains record metadata. Modbus register rows preserve exact raw words.

An `interaction` event has `{interaction_id,type:"confirm|prompt|select",title,request?,options?,default?,sensitive?}`. Reply using `respond {interaction_id,confirmed,value?}`. Choice values must equal an offered choice, not an arbitrary index. Answers are one-use. Dialog payloads above the bounded event size fail before execution. Cancel/timeout removes the interaction.

## Independent OPC UA subscriptions

Running an OPC `subscribe` preview returns `{run_id,subscription_id,background:true}` without occupying the foreground slot. Up to 16 independent subscriptions can coexist with reads, browse and other foreground operations. Adding another subscription is also permitted during a foreground read.

- `subscriptions.list` → `[{id,request_id,endpoint,node_ids,started,status,error?,last_event?}]`
- `subscriptions.stop {subscription_id}` → `{cancelled:count}`
- `subscriptions.stop-all` → `{cancelled:count}`

Events are `subscription.started`, `subscription.event` with `{subscription_id,kind,data}`, and `subscription.done` with `{subscription_id,status,error}`. Lifecycle pause stops them all without later replay. Completed rows are retained only within a bounded 32-row registry.

`opcua.connections` returns up to 100 successful endpoint records with `{endpoint,node_id,security_policy,security_mode,server_cert_sha256,last_connected}`. Passwords, usernames, certificates and payloads are never saved in this file. `opcua.connections.clear {confirmed:true}` removes this metadata.

`opcua.identity {cert_path,key_path,application_uri,confirmed:true}` starts a cancellable local task. It creates a new certificate/private-key pair without overwriting either file and emits `identity {cert_path,key_path,application_uri}`. Paths remain inside the private root.

## HTTP, crypto and history utilities

- `http.curl {request_id}` or `{request}` → `{curl}`. Does not execute the shell or network dependencies. This explicit output can contain request credentials; show an appropriate private-output warning
- `http.filter {query,data}` → result array using the shared bounded query engine
- `crypto.convert {direction:"encode|decode",data,codec}` → `{text?,base64,bytes}`. Codec uses shared fields `algorithm`, `key:{value,encoding}`, `iv:{value,encoding}`, padding, plaintext/ciphertext encodings and `base64_decode` options
- `crypto.transform {data,codecs:{id:Codec},rules:[Rule]}` → the same binary-safe result shape
- `history.list {request_id?}` → current collection's history rows
- `history.get {history_id}` → full entry including body and raw-body Base64
- `history.delete {ids:[integer],confirmed:true}` → `{deleted}`; blocked by read-only mode
- `history.collections` → collection metadata
- `history.query {sql}` → read-only result sets
- `history.preview {sql}` → `{database,sql,token,backup_bytes,statements}`
- `history.execute {sql,token,backup,confirmed:true}` → `{backup,results}`. Requires unchanged preview and a new private backup path. Blocked by read-only mode

The core API requires the explicit `History` option; Flutter hosts enable it by default while preserving saved opt-outs. CLI/TUI persistence remains opt-in. Neither startup nor an ordinary history list creates an empty history database. HTTP workflow options preserve separate prompts, selections, TLS approvals, root-write authorization and dependency-write authorization. Optional engine sandbox hooks enforce private files after dynamic rendering and before file reads/network execution; desktop callers retain original behavior when hooks are nil.

## Modbus local tools

- `modbus.encode {kind,value,word_order,address}` → exact encoded uint16 words
- `modbus.interpret {request_id|request,words:{"address":uint16}}` → annotated rows. Only one response's words should be supplied
- `modbus.rules {request_id|request}` → validated rule definitions
- `modbus.pause` / `modbus.resume` → `{paused}` for the current read loop. These preserve the original sample budget/deadline and cannot queue or replay writes
- `modbus.stats` → `{operations,success,failure,duration_ms,last}`. `modbus.metrics` events include timestamp, action, unit, address, count, duration_ms, write/success/cancelled/error_class
- `modbus.import {source,prefix}` → MTUI full-configuration conversion plan, without saving or executing
- `modbus.registers.import {source,kind}` → converted register parameters
- `modbus.registers.export {request_id|request}` → `{source}`
- `modbus.write-log {path}` → bounded audit entries
- `modbus.discovery.preview {target,port,timeout_ms,concurrency,method:"tcp|ping"}` → `{token,targets,method,port,timeout_ms,concurrency,notice}`
- `modbus.discovery.run {token,confirmed:true}` → asynchronous `run_id`; `discovery` events contain `{address,open,error,completed,total}`. The exact finite target set is preview-bound; an open port is not proof of a Modbus device
- `modbus.controller.start {request_id|request,listen,scope?,confirmed:true}` → asynchronous `run_id`. Numeric loopback listening only. Scope uses `{Unit,Address,Count,Type:"holding|coil"}`; omitted scope is read-only. Go read-only mode blocks a write scope. Cancel stops the server. USB endpoints use the same explicitly selected/permitted platform transport, inherited through the controller request context
- `modbus.snapshot.save {path,snapshot,confirmed:true}` saves a new immutable file; `modbus.snapshot.load {path}` loads one
- `modbus.snapshot.diff {before,after}` → exact raw register differences; endpoint/unit/register-space mismatches fail
- `modbus.csv.diff {source,kind,words,hex_address}` → raw CSV differences; kind is the CSV register space

Snapshot schema: `{version:1,endpoint,unit,action:"read-holding|read-input",time:"2026-10-01T04:00:00Z",values:{"0":123,"1":65535}}`. `words` uses the same numeric-string-key map.

## App-private files and USB

`files.list` returns up to 1000 private regular-file rows `{name,path,bytes,modified}`. `file.read {path}` returns `{text?,base64,bytes}` up to 4 MiB. `file.write {path,data,confirmed:true}` writes a private text file; use `config.save` for the active collection. The history database cannot be overwritten by the generic file utility. Paths reject traversal, URI paths and symlinks. Collection cross-file references remain confined to the private root; tilde references are rejected.

Android SAF selection should copy file streams into private storage and keep explicit export/share in Java. No broad external-storage path or executable helper is used. Platform `usb://deviceId/portIndex` requests use only the injected, explicitly selected/permitted transport. Desktop `rtu://` device paths are rejected on this bridge. TLS/key/upload/download/audit paths are all private-scoped.

## Full large-result inspection

Large events include `result_id` alongside their bounded inline preview. `result.get {result_id}` returns `{result_id,run_id,kind,data}` with the original full event data and raw binary metadata intact. The cache is memory-only, contains at most 8 entries / 32 MiB, and permits individual data up to just under the 16 MiB reply ceiling. Poll reports `evicted_result_ids` and `evicted_result_count` when older entries leave the cache. Looking up an expired result fails explicitly. Results beyond the individual ceiling carry `result_unavailable_reason`; narrow the request in that case. Closing the session clears this cache. Explicit sharing/export stays in the Android document-picker flow.

`history.collection.preview {kind:"migrate|rename|merge|delete",source,target?}` creates the exact collection SQL and returns the standard history transaction preview. Pass its SQL/token/new backup path to `history.execute`; rename and merge use a metadata migration, not filesystem moves. `http.curl {request_id,execute_triggers:true,confirmed:true}` explicitly opts into dependency execution and returns an asynchronous run ID with a `curl {curl}` event; mutating dependencies still require their own dialogs.

## Final native interaction details

- `respond {interaction_id,confirmed,selection_index}` chooses the original offered value by index. The index preserves numeric versus string identity even when large integers have an exact string wire representation. Prompt text continues to use `value`.
- Modbus `preview` includes `protocol_preview`: validated function code, exact unit/address/range, encoded register words or coil values, and FC23 read/write ranges. No transport is opened by preview.
- A Modbus `started` event includes `source {request_id,endpoint,unit,action}` from the exact resolved request. Cache/snapshot consumers must bind it to `run_id`, retain it outside rolling event lists and never substitute a currently edited request.
- `modbus.stats` retains per-operation fields and `session {reads,writes,errors,cancelled,success,duration_ms}` accumulated in memory. Cancellation during a sampling wait counts once; cancelled I/O is not counted twice.
- `modbus.import {source,prefix}` returns a reviewed `token`, four read-only request definitions and warnings. `modbus.import.apply {token,request_ids:[1–4 selected IDs],confirmed:true}` appends only reviewed IDs, rejects stale/changed source and collisions, creates an exclusive original-byte backup, then saves atomically. The returned state contains the actual `backup` path. Backup failure leaves the active file unchanged.
- `config.switch` preview exposes its existing `digest`, covering the source and parsed references. Saving a draft invalidates a token: a UI must preview again and compare digests before proceeding under an earlier review.
- `http.filter.start {query,data}` runs a bounded local jq query asynchronously, returns a `run_id`, emits `query` and `done`, and supports exact-run `cancel`. The synchronous `http.filter` remains available.
- Kafka record events preserve `raw_key_base64`, `raw_value_base64`, `key_is_null` and `value_is_null` alongside decoded values, headers and timestamp.

OPC UA 读取中的非有限 IEEE 浮点值以 `NaN`、`+Infinity`、`-Infinity` 文本传输，并保留 `value_type_name`、质量状态和时间戳；浮点数组保留原位置，避免因 JSON 不支持非有限数字而丢弃整个设备读值事件。写入值的校验范围保持不变。

## Database capacity and retention

`history.status` additionally returns `storage`: `entries`, `payload_bytes`, actual `database_bytes`, `sidecar_bytes`, `total_bytes`, `reusable_bytes`, and database-wide `policy`. Reading never creates a missing database. `policy` has `mode` (`stop` or `prune`), `max_age_days`, `max_entries`, `max_bytes`; 0 means unbounded. Default is stop / 0 days / 10000 entries / 134217728 content bytes. Counts include all collections.

- `history.retention.preview {policy}` returns a five-minute token and explicit deletion/retained counts and content bytes
- `history.retention.apply {token,confirmed:true}` requires unchanged data and policy. `prune` authorizes immediate and subsequent automatic permanent deletion; `stop` never removes existing records
- `history.compact {confirmed:true}` explicitly reclaims unused SQLite pages while idle. It requires temporary disk space and may block writers; it never removes live records

Mutations are blocked by read-only protection. A capacity-limited response still completes normally and emits `history_status` with reason `capacity`. Read-only notices are emitted only for an actual response that otherwise qualifies for persistence, including eligible dependency requests. Transport failures and `persist:false` emit no such notice.
