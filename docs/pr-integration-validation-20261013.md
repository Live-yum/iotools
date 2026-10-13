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

## First integration CI findings (`f15ed14`)

The first exact-head run is retained, including its failures:

- [Windows TUI](https://github.com/Live-yum/iotools/actions/runs/38380262015/job/115238643516)
  exposed an LF-only test-fixture edit. Windows checkout preserved CRLF, so the
  test accidentally left `default_profile` declared. The fixture now removes
  that field through the YAML model and covers both LF/CRLF. A source overlay
  reproduced the original failure and passed with the correction; production
  profile behavior is unchanged.
- [Android/Flutter widget checks](https://github.com/Live-yum/iotools/actions/runs/38380262075/job/115238683703)
  exposed a framework wrapper issue shared by desktop test jobs. BackButton has a
  null outer callback but builds an enabled inner IconButton with Navigator's
  default action. Kafka now selects the actual IconButton instead of the wrapper.
  Ordinary disabled buttons must still wait until enabled; no shared readiness
  gate or business assertion was removed.
- [Unsigned iOS device verification](https://github.com/Live-yum/iotools/actions/runs/38380262019/job/115238700145)
  rejected missing essential Mach-O files. Captured metadata and logs prove that
  Flutter automatically migrated the existing CocoaPods integration to Swift
  Package Manager, whose production plugin libraries are static. The failed run
  did not retain the exact missing-path inventory, so the layout mismatch is an
  inference to confirm in fresh CI. Preserve the repository's existing integration
  explicitly rather than weaken the binary gate. This unsigned artifact is not physical-device runtime acceptance.
- [iOS simulator build](https://github.com/Live-yum/iotools/actions/runs/38380262019/job/115238700120)
  exited 255 without retained build diagnostics. Capture verbose output with
  pipefail on the next run. Its exact failure cause is not inferred solely from
  the device packaging issue.

The first integration's Fast/security checks, release unit gates, Linux TUI
checks and web/browser end-to-end checks passed. They do not override the failures
above. Corrections require fresh exact-head platform verification before merge.

### Follow-up local verification

A subsequent normal official-SDK dependency invocation completed precache through
its original source; no alternate download, permission change or synthetic cache
marker was used. This allowed the previously unavailable local Flutter checks:

- Focused actual Flutter tests: 29 passed, including real rendered BackButton
  selection, the disabled ordinary IconButton gate, readiness redirect rejection,
  IME/scrolling and the full OPC attribute list.
- Full Flutter test suite: 209 passed, seven existing environment-dependent skips,
  no failures. Skips are not runtime acceptance.
- Full `flutter analyze --no-pub`: no issues.
- The intentionally failing diagnostics fixture exited 1 and its verifier
  confirmed the original failure/stack and failed outcome were preserved.
- Enforced lockfile resolution left the lock unchanged. The real generated
  Flutter plugin metadata passed the Android dependency/plugin audit and records
  SwiftPM disabled for iOS/macOS. This is still not Gradle/Maven/APK acceptance.
- The corrected Windows profile tests passed ten repetitions, the full TUI race
  suite, Windows amd64 test-program cross-compilation, and the CRLF overlay
  before/after reproduction. Actual Windows execution remains a CI requirement.
- iOS-specific Python tests, CI contracts and the full platform Python suite
  passed without removing production binary or actual XCTest gates.

Fresh Windows, Android, desktop and Apple runtime checks are required for this
follow-up commit. The iOS simulator's prior exit 255 is not declared resolved
before those checks run with the improved captured diagnostics.
## Second integration findings (`8238332`)

The second round passed both Windows/Linux TUI workflows, HTTP history, and the
native desktop/web/iOS checks reported by their exact-head jobs. In particular,
the Windows CRLF fixture and unsigned iOS production Mach-O gate now passed;
the iOS simulator also reached and passed XCTest. This verifies recovery without
claiming a proven root cause for the earlier simulator exit 255.

[Android run 38382139335](https://github.com/Live-yum/iotools/actions/runs/38382139335/job/115244982764)
failed the exact JNI APK closure gate after building the compact AOT APK: the
plugin contributed `lib/armeabi-v7a/libdartjni.so` although the requested Go and
Flutter targets were ARM64/x86_64. The gate correctly rejected this extra ABI.
Flutter's default non-split ABI set includes ARMv7; the application now finalizes
non-split dependency filters from the explicitly requested supported targets.
Split APKs retain Flutter's own split configuration. The strict APK gate and its
ELF/class checks are unchanged; an extra-ARMv7 negative fixture is added.
Actual corrected APK packaging and runtime acceptance remain pending fresh CI.

New review findings are being addressed before merge: partial per-node read
errors must not abandon later 10,000-node chunks, mobile scans need a bounded
lossless result path independent of the event queue, mobile read batch size must
be editable, invalid TUI limits must not be silently coerced, unavailable read
events must explicitly report no value, and the standalone scan example must
select its declared local profile. These findings are not waived by green jobs.

### Review corrections after the second round

- An explicit typed partial-read error permits later selection chunks to run,
  while cancellation, identity, session/transport and unknown errors stop them.
  Earlier chunk errors remain visible after later successes. Missing read values
  always carry `has_value: false`.
- Mobile scan records are captured before the lossy event queue. A run-bound
  read-only pagination API exposes up to 100,000 nodes / 16 MiB within the existing
  combined 32 MiB result-payload budget. Oversize data or limits stop with explicit
  incomplete status; a new run or close invalidates the cursor. Poll/state retain
  the descriptor even if all lifecycle events were evicted.
- Flutter displays one bounded page at a time, with exact node/parent identifiers,
  completion status and explicit failure details. Late replies cannot populate a
  changed run, collection, session lifecycle or disposed page. Long errors and
  paging controls remain usable on a 320×320 test surface.
- Mobile `read_batch_size` is an integer field with the engine's default 100 and
  allowed range 1..1000. TUI raw scan-limit values are validated before opening or
  cancelling anything; invalid values are not coerced to defaults. Collection
  mode still permits later valid services after a bad first target, without
  resolving variables twice or rewriting the bad configuration.
- The standalone scan example selects `default_profile: local`; its regression
  resolves the documented default invocation without contacting any endpoint.

A disposable local OPC UA server test retained and paged 601 actual protocol
nodes despite event-queue loss. Synthetic 10,000-node stress, size bounds and
concurrent lifecycle tests exercise the transport/cache design; neither is
industrial device or physical Kylin acceptance. Old-source overlays reproduce
both the cross-chunk read and strict-limit bugs, then pass with the fixes.

### Final local checks for this correction tree

- Full Go ordinary and race suites: each 979 passing test/subtest results and
  three explicit environment/opt-in skips; no failures. Go source hashes were
  frozen across both final suites. Full vet, module verification/tidy-diff and
  current govulncheck passed, with no reported vulnerabilities.
- Full Flutter suite: 224 passed, seven existing skips, no failures; analyze clean.
  Lockfile SHA-256 remains
  `2cdd963a4a5305ddff4ad7d0a5bf36ba2e1a15db578c74fb207240aaf8e73ccd`.
- Python CI/release/Flutter/platform suites passed 24/67/74/124 tests. These
  include 17 dependency-audit unit tests, not an actual rebuilt APK.
- Independent final source review found no remaining confirmed blocker in these
  corrections. New remote exact-head checks are still required before merge;
  the earlier Android failure remains preserved and does not count as passed.

## Third-round test-fixture cleanup failure (`00321e9`)

[Release-unit run 38385801911](https://github.com/Live-yum/iotools/actions/runs/38385801911/job/115257169285)
failed `SourceTests.test_compiler_config_rejected` during strict temporary-directory
cleanup with `ENOTEMPTY: objects`, not because its compiler-config rejection
assertion failed. Git Trace2 on the original fixture captured detached automatic
maintenance after commits, providing a concrete cleanup-race risk. The original
CI filesystem writer was not traced, so this is not claimed as an exact syscall
reproduction of that failure.

Only these disposable fixture repositories now set local `maintenance.auto=false`
and `gc.auto=0` before the first commit. User/global repository settings and strict
cleanup are unchanged. A real-commit Trace2 regression fails with the old fixture
and passes with the change; 650 repeated source tests (850 commits) with inherited
auto-maintenance enabled observed no maintenance/GC children after the local
settings. The full release-unit suite now passes 68 tests. Independent source
review confirms Git 2.55 checks `maintenance.auto` before launching maintenance.
Fresh CI on this correction remains required; the failed run is retained.

### Third-round Android outcome and history lifecycle correction

[Android job 115257225780](https://github.com/Live-yum/iotools/actions/runs/38385801818/job/115257225780)
passed the strict JNI/ABI APK gate, independent ARM64 packaging, OPC UA suite
(two tests), and the exact normal-entry AOT APK instrumentation test. The main
protocol suite remained **failed** (five passed, one failed): a single HTTP AES
request produced a 200 response/transformation, then history persistence returned
`sql: transaction has already been committed or rolled back`. These successful
substeps do not override the failed suite.

The recipe's network/transform deadline was also controlling the subsequent
history transaction. A deterministic real loopback HTTP + SQLite regression
reproduces deadline expiry after receiving the full response; the old source
fails and the corrected source stores the complete raw/transformed record once.
History persistence now has its own bounded five-second context under the prior
caller context. Explicit caller cancellation and outer chained-request deadlines
still stop it. There is no HTTP resend or transaction retry. Capacity errors do
not hide a simultaneous cancellation, and SQL failures retain the genuine context
cause without being mislabeled by the later cleanup cancel.

Five lifecycle regressions pass. Full engine/mobileapi race checks pass with both
drivers: 260 top-level tests on the default driver and 264 on the Android bundled
driver, each with two pre-existing explicit skips (ICMP permission and opt-in
three-process SQLite interoperability). Both vet passes, formatting and independent
review passed. This identifies and corrects a reproducible lifecycle defect; the
original Android log did not record the exact context-expiry instant. Actual
Android UI acceptance of this correction still requires fresh exact-head CI.
