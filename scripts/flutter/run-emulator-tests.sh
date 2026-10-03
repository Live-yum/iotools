#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
mkdir -p android-evidence/flutter-screenshots
fixture_pid=
graphics_watchdog_pid=
first_failure=0
# Diagnostics never turn a failed test green or request elevated device access.
# Each read is bounded; permission-denied native traces remain recorded evidence.
graphics_snapshot() (
 trap - EXIT
 set +e
 folder="android-evidence/graphics-$1"
 mkdir -p "$folder"
 date -u +%FT%TZ > "$folder/captured-utc.txt"
 read_evidence() {
  output=$1; shift
  timeout --kill-after=1s 4s "$@" > "$folder/$output" 2>&1
  printf '%s exit=%s\n' "$output" "$?" >> "$folder/exit-status.txt"
 }
 read_evidence logcat.txt adb logcat -d
 timeout --kill-after=1s 4s adb exec-out screencap -p > "$folder/screen.png" 2> "$folder/screen-error.txt"
 printf 'screen.png exit=%s\n' "$?" >> "$folder/exit-status.txt"
 read_evidence window.txt adb shell dumpsys window windows
 read_evidence surfaceflinger.txt adb shell dumpsys SurfaceFlinger
 read_evidence gfxinfo.txt adb shell dumpsys gfxinfo io.github.liveyum.iotools
 pid=$(timeout --kill-after=1s 4s adb shell pidof io.github.liveyum.iotools 2> "$folder/pid-error.txt")
 pid=${pid//$'\r'/}
 if [[ "$pid" =~ ^[0-9]+$ ]]; then
  read_evidence threads.txt adb shell ps -T -p "$pid"
  read_evidence native-backtrace.txt adb shell debuggerd -b "$pid"
 else
  printf 'No single target process: %s\n' "$pid" > "$folder/native-backtrace.txt"
 fi
 exit 0
)
stop_graphics_watchdog() {
 if test -n "$graphics_watchdog_pid"; then
  kill -TERM "$graphics_watchdog_pid" 2>/dev/null || true
  wait "$graphics_watchdog_pid" 2>/dev/null || true
  graphics_watchdog_pid=
 fi
}
start_graphics_watchdog() {
 local label=$1 delay=$2
 (
  trap - EXIT
  timer=
  trap 'test -z "$timer" || kill -TERM "$timer" 2>/dev/null; exit 0' TERM INT
  # Capture before the unchanged suite deadline can kill a hung app.
  sleep "$delay" & timer=$!
  wait "$timer" || exit 0
  timer=
  graphics_snapshot "$label"
 ) &
 graphics_watchdog_pid=$!
}
collect() {
 result=$?
 if test "$first_failure" -ne 0; then result=$first_failure; fi
 set +e
 stop_graphics_watchdog
 adb logcat -d > android-evidence/flutter-logcat.txt
 adb exec-out screencap -p > android-evidence/flutter-last-screen.png
 for port in 48411 48413 48416; do curl --max-time 3 -s "http://127.0.0.1:$port/metrics" > "android-evidence/fixture-metrics-$port.json"; done
 test -z "$fixture_pid" || kill -TERM "$fixture_pid"
 for port in 48410 48411 48412 48413 48414 48415 48416; do adb reverse --remove "tcp:$port"; done
 exit "$result"
}
trap collect EXIT
adb shell wm density 160
# Boot completion can precede a stable package manager/API response under
# software emulation. Require actual readiness before Gradle caches device data.
ready_samples=0
ready_deadline=$((SECONDS+60))
for attempt in $(seq 1 30); do
 sdk=
 package_path=
 if sdk=$(timeout 5s adb shell getprop ro.build.version.sdk 2>>android-evidence/device-ready.log); then sdk=${sdk//$'\r'/}; fi
 if package_path=$(timeout 5s adb shell cmd package path android 2>>android-evidence/device-ready.log); then package_path=${package_path//$'\r'/}; fi
 printf 'attempt=%s sdk=%s package=%s\n' "$attempt" "$sdk" "$package_path" >> android-evidence/device-ready.log
 if [[ "$sdk" =~ ^[0-9]{2,3}$ ]] && ((10#$sdk>=26)) && [[ "$package_path" == package:* ]]; then
  ready_samples=$((ready_samples+1))
 else
  ready_samples=0
 fi
 if test "$ready_samples" -eq 3; then break; fi
 if test "$SECONDS" -ge "$ready_deadline"; then break; fi
 sleep .2
done
if test "$ready_samples" -ne 3; then echo "Android API/package manager did not become consistently ready"; exit 75; fi
timeout 5s adb features > android-evidence/adb-features.txt
# Record the renderer actually selected by the emulator, not just its requested mode.
if test -x "${ANDROID_HOME:-}/emulator/emulator"; then
 timeout --kill-after=1s 4s "$ANDROID_HOME/emulator/emulator" -version > android-evidence/emulator-boot-version.txt 2>&1 || true
fi
timeout --kill-after=1s 4s adb shell getprop ro.hardware.egl > android-evidence/emulator-egl.txt 2>&1 || true
renderer_result=0
timeout --kill-after=1s 4s adb shell dumpsys SurfaceFlinger > android-evidence/emulator-surfaceflinger.txt 2>&1 || renderer_result=$?
printf '\nadb shell dumpsys SurfaceFlinger exit=%s\n' "$renderer_result" >> android-evidence/emulator-surfaceflinger.txt
fixture="${RUNNER_TEMP:-/tmp}/iotools-flutter-fixtures"
go build -o "$fixture" ./scripts/android-fixtures
"$fixture" -ready "$PWD/android-evidence/fixture-ready.json" > android-evidence/fixture.log 2>&1 &
fixture_pid=$!
for n in $(seq 1 50); do test ! -s android-evidence/fixture-ready.json || break; kill -0 "$fixture_pid"; sleep .2; done
test -s android-evidence/fixture-ready.json
for port in 48410 48411 48412 48413 48414 48415 48416; do adb reverse "tcp:$port" "tcp:$port"; done
# Real Android document provider and bounded byte-stream tests.
(cd mobile/android && timeout --kill-after=30s 5m ./gradlew --no-daemon --max-workers=1 :app:connectedDebugAndroidTest)
# All protocol UI interactions run inside the Flutter app, backed by real local fixtures.
# Release builds remove dev plugins from the generated registrant. Let drive run
# its official debug tooling regeneration; --no-pub would retain that release registrant.
set +e
# Keep independent app processes: a timed-out test cannot leak a late pump or
# cleanup into another protocol suite. Both exits remain authoritative failures.
for suite in main opcua; do
 target=integration_test/app_test.dart
 budget=25m
 diagnostics_delay=1440
 expected_business_tests=5
 expected_app_passes=6 # Flutter's final count includes tearDownAll.
 if test "$suite" = opcua; then target=integration_test/opcua_workflow_test.dart; budget=10m; diagnostics_delay=420; expected_business_tests=1; expected_app_passes=2; fi
 adb shell am force-stop io.github.liveyum.iotools
 stop_result=$?
 if test "$stop_result" -ne 0; then
  if test "$first_failure" -eq 0; then first_failure=$stop_result; fi
  printf '%s\n' "$stop_result" > "android-evidence/flutter-$suite-stop-failure.txt"
  break
 fi
 start_graphics_watchdog "$suite-pretimeout" "$diagnostics_delay"
 # integration_test retains all results/screenshots until the host requests
 # them. Do not make its startup depend on drive's default debugger pause:
 # a recorded run stalled before its first test in the isolate-runnable RPC.
 # The pinned Flutter driver supports an already running isolate. This is a
 # launch mitigation, not a proven VM fix; every app-side gate below still runs.
 (cd mobile && timeout --kill-after=30s "$budget" flutter drive --driver=test_driver/integration_test.dart --target="$target" --no-start-paused --no-enable-impeller --dart-define=IOTOOLS_TEST_FIXTURES=true) 2>&1 | tee "android-evidence/flutter-$suite.log"
 pipeline_results=("${PIPESTATUS[@]}")
 flutter_result=${pipeline_results[0]}
 driver_result=$flutter_result
 log_result=${pipeline_results[1]}
 stop_graphics_watchdog
 # The extended driver can return zero during an app-side timeout/teardown race.
 # Require the app's own final test summary and exact count, not the host's
 # premature "All tests passed.". Only structured suite failures/timeouts match;
 # expected validation errors printed inside a passing test do not fail this gate.
 app_prefix='^I/flutter[[:space:]]*\([[:space:]]*[0-9]+[[:space:]]*\):[[:space:]]*'
 app_progress="${app_prefix}([0-9]+:)+[0-9]{2}[[:space:]]+"
 app_failure="${app_progress}\+[0-9]+[[:space:]]+-[1-9][0-9]*:[[:space:]]+Some tests failed\.[[:space:]]*$"
 app_timeout="${app_prefix}TimeoutException after [0-9:.]+: Test timed out after [0-9]+ (seconds?|minutes?|hours?)\.[[:space:]]*$"
 app_success="${app_progress}\+${expected_app_passes}:[[:space:]]+All tests passed![[:space:]]*$"
 app_status=missing_or_wrong_completion
 if LC_ALL=C grep -Eq "$app_failure|$app_timeout" "android-evidence/flutter-$suite.log"; then
  app_status=failed
 elif LC_ALL=C grep -Eq "$app_success" "android-evidence/flutter-$suite.log"; then
  app_status=passed
 fi
 if test "$flutter_result" -eq 0 && test "$app_status" != passed; then flutter_result=1; fi
 {
  printf 'driver_exit=%s\napp_outcome=%s\nexpected_business_tests=%s\nexpected_app_passes_including_teardown=%s\neffective_exit=%s\n' "$driver_result" "$app_status" "$expected_business_tests" "$expected_app_passes" "$flutter_result"
  LC_ALL=C grep -E "${app_progress}\+[0-9]+([[:space:]]+-[0-9]+)?:[[:space:]]+(All tests passed!|Some tests failed\.)[[:space:]]*$" "android-evidence/flutter-$suite.log" || true
 } > "android-evidence/flutter-$suite-outcome.txt"
 python3 scripts/flutter/verify-mobile.py mobile/build/app/outputs/flutter-apk/app-debug.apk --integration-test
 plugin_result=$?
 if test "$first_failure" -eq 0; then
  if test "$flutter_result" -ne 0; then first_failure=$flutter_result; elif test "$log_result" -ne 0; then first_failure=$log_result; elif test "$plugin_result" -ne 0; then first_failure=$plugin_result; fi
 fi
 printf '%s %s\n' "$flutter_result" "$plugin_result" > "android-evidence/flutter-$suite-exit.txt"
done
set -e
# Verify the exact normal-entry AOT delivery file, independently of the debug test entrypoint.
aot="$(find android-dist -maxdepth 1 -name '*universal*-aot-test-signed.apk' -print -quit)"
test -n "$aot"
aot_sha="$(sha256sum "$aot" | cut -d ' ' -f 1)"
adb shell am force-stop io.github.liveyum.iotools
adb install -r "$aot"
adb install -r android-evidence/aot-test/instrumentation.apk
set +e
start_graphics_watchdog aot-pretimeout 420
timeout --kill-after=20s 10m adb shell am instrument -w -r -e class io.github.liveyum.iotools.AotAcceptanceTest -e apk_sha256 "$aot_sha" -e build_sha "$IOTOOLS_SHA" io.github.liveyum.iotools.test/androidx.test.runner.AndroidJUnitRunner > android-evidence/aot-instrumentation.txt 2>&1
aot_result=$?
stop_graphics_watchdog
if test "$first_failure" -eq 0 && test "$aot_result" -ne 0; then first_failure=$aot_result; fi
set -e
cat android-evidence/aot-instrumentation.txt
mkdir -p android-evidence/aot-device
adb pull /sdcard/Android/data/io.github.liveyum.iotools/files/aot-evidence/. android-evidence/aot-device/
if test "$aot_result" -ne 0; then exit "$aot_result"; fi
python3 scripts/flutter/verify-aot-device.py "$aot" android-evidence

# Independent AOT evidence does not turn a failed Flutter suite into a green job.
exit "$first_failure"
