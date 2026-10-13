# PR #4–#11 integration validation (2026-10-13)

This integration preserves the original commits of all eight open pull requests.
It resolves their shared SDK/dependency conflicts in one source tree so the final
combination, rather than eight incompatible historical snapshots, is validated.
No release publication, deployment, new credentials, or branch-rule changes are
part of this work.

## Original heads retained

| PR | Original head | Scope |
| --- | --- | --- |
| #4 | `4aa9d92d2fd72a4c0eb09b118c7a3e173fc0657b` | Android path provider 2.3.1 |
| #5 | `0d6813359ce24a531cfe61788e8718422b064e6f` | Path provider 2.1.6 |
| #6 | `a6a978ee8dd28a36933ef2592e6dd0456333527a` | Cupertino icons 2.0 and compatible pinned Flutter SDK |
| #7 | `2d6dfb8ab9725bca96bcd142b54d2a0fdcfaab3a` | Official Actions upgrades |
| #8 | `dff22ed0429d24892802db62ab7594cf95146126` | x/net upgrade |
| #9 | `38b8eafb9d206ddac256556e70e880bba62b8c1c` | goavro 2.15.0 |
| #10 | `8dc75cebee834dd3b88f9c0e3ed516bad3a8a288` | x/sys 0.49.0 |
| #11 | `b4b8e7efc295a6e93abe1bc0f1d755a5ac0bf2ef` | OPC UA scan/export/recording |

## Integration fixes

- Keep Go 1.27.2 and advance x/net to 0.60.0, the fixed version required by the
  current security database. Keep x/sys 0.49.0 and goavro 2.15.0.
- Resolve the Flutter lockfile using the exact official Flutter 3.44.9 / Dart
  3.12.2 SDK. Record the restored JNI dependency closure explicitly; do not pretend
  the old no-JNI isolation still applies. Preserve the original audit baseline
  and constrain reviewed lock hashes, dependency/plugin graphs, Maven coordinates,
  APK classes, native ABIs and 16 KiB ELF load-segment compatibility.
- Keep the existing reviewed Android NDK pin for the JNI subproject using AGP's
  final DSL callback; do not patch downloaded package sources.
- Select the basic Gradle Actions cache explicitly. Do not load the new enhanced
  proprietary caching component by default.
- Embedded `--init` and public local examples select `default_profile: local`.
  Existing files are not overwritten. An old generated file needs that top-level
  setting or an explicit `--profile local`. Structural `--validate` semantics
  remain unchanged; generated-config tests additionally exercise variable resolution.
- F6's explicit no-profile selection remains empty for HTTP execution, curl
  preview and temporary overrides, without mutating the stored collection.
- Kafka test interactions use the existing centered, current-route, enabled,
  hit-test-aware controls and IME lifecycle helper. Protocol assertions remain.
- Fixture startup recovery retries only a loopback `/ready` GET transport error,
  at most five attempts within 15 seconds. Redirects, HTTP errors and invalid JSON
  fail immediately. Metrics and business actions are never retried by this helper.
- TUI subscription-test observer polls no longer request redundant full-screen
  redraws. All assertions and deadlines remain unchanged; the MQTT outage test
  was not changed.
- SDK/dependency changes now select the complete existing Flutter platform matrix
  even on ordinary PRs. Existing candidate-label gates remain. No runtime gate is
  removed. Android also proves that the deliberately failing diagnostics fixture
  remains a failure before running real suites.

## Local evidence

On the final frozen Go sources, with Go 1.27.2:

- `go mod verify`, `go mod tidy -diff`, gofmt and full `go vet ./...`: passed.
- Full ordinary and race suites, sequential package execution with the original
  five-minute per-package timeout: each 827 passing test/subtest results,
  515 passing top-level tests and 11 passing packages; two packages have no tests.
- Three aggregate skips: unprivileged ICMP socket permission, optional visual
  capture, and SQLite multi-process exchange. The SQLite exchange subsequently
  passed its dedicated three-process race-enabled create/migrate/verify invocation.
  ICMP and optional visual capture remain unexecuted, not passed.
- Android SQLite-tagged engine/mobileapi race tests: passed.
- Final govulncheck v1.8.0 using the current vulnerability database: no vulnerabilities.
- Static Linux AArch64 TUI cross-build: passed. This is not physical Kylin ARM64
  runtime acceptance.
- Init/F6 focused ordinary/race tests and the old-source negative control passed:
  all four no-profile regression branches fail with the original HTTP outlets.
- Readiness helper: 15 standalone Dart checks passed, including dropped headers,
  bounded failure, invalid bodies and four redirect statuses with zero target hits.
- Actual Dart package resolution, enforced-lockfile resolution and the 66-package
  graph passed the exact dependency audit. Whole-mobile Dart analysis passed.
- Python contract suites: CI 23, release 67, Flutter harness 73 and platform
  harness 121 passed. The Android dependency audit includes 16 passing tests.
  Synthetic plugin/APK/DEX/ELF fixtures are unit checks only.

Local Flutter precache could not download `font-subset.zip` because the network
proxy rejected the request (HTTP 403). No alternate download or fake cache marker
was used. Dependency resolution used the already installed official Dart SDK,
which does not require that graphics tool. Local full Flutter/widget/Android GUI
acceptance is therefore **not executed**. Actual Flutter plugin generation,
Maven resolution, packaged APK validation and runtime acceptance must pass in CI
on the integration commit before merge.

## Historical failures are retained

The original [PR #11 Android failure](https://github.com/Live-yum/iotools/actions/runs/38345378579)
remains visible: Kafka's tap did not hit its control and the separate OPC startup
readiness connection closed before response headers. A successful AOT substep did
not make that run successful. The transport failure's exact root cause is still
unproven; the bounded readiness change does not change any protocol assertion.

The original [PR #10 security failure](https://github.com/Live-yum/iotools/actions/runs/38034930870)
required the Go/x/net updates above. Other old-head Android failures and the
previous intermittent MQTT/TUI test failures are not erased by newer results.

Fresh remote checks are pending when this record is committed. Historical green
runs cannot substitute for the final integration commit. The field EMS discovery
error and physical industrial/Kylin device behavior are not covered by loopback,
widget, cross-build or hosted CI evidence. No private industrial endpoint or
configuration is included in this integration.
