#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
mkdir -p android-evidence/flutter-screenshots
fixture_pid=
first_failure=0
collect() {
 result=$?
 if test "$first_failure" -ne 0; then result=$first_failure; fi
 set +e
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
 if test "$suite" = opcua; then target=integration_test/opcua_workflow_test.dart; budget=10m; fi
 adb shell am force-stop io.github.liveyum.iotools
 stop_result=$?
 if test "$stop_result" -ne 0; then
  if test "$first_failure" -eq 0; then first_failure=$stop_result; fi
  printf '%s\n' "$stop_result" > "android-evidence/flutter-$suite-stop-failure.txt"
  break
 fi
 (cd mobile && timeout --kill-after=30s "$budget" flutter drive --driver=test_driver/integration_test.dart --target="$target" --no-enable-impeller --dart-define=IOTOOLS_TEST_FIXTURES=true) 2>&1 | tee "android-evidence/flutter-$suite.log"
 pipeline_results=("${PIPESTATUS[@]}")
 flutter_result=${pipeline_results[0]}
 log_result=${pipeline_results[1]}
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
timeout --kill-after=20s 10m adb shell am instrument -w -r -e class io.github.liveyum.iotools.AotAcceptanceTest -e apk_sha256 "$aot_sha" -e build_sha "$IOTOOLS_SHA" io.github.liveyum.iotools.test/androidx.test.runner.AndroidJUnitRunner > android-evidence/aot-instrumentation.txt 2>&1
aot_result=$?
if test "$first_failure" -eq 0 && test "$aot_result" -ne 0; then first_failure=$aot_result; fi
set -e
cat android-evidence/aot-instrumentation.txt
mkdir -p android-evidence/aot-device
adb pull /sdcard/Android/data/io.github.liveyum.iotools/files/aot-evidence/. android-evidence/aot-device/
if test "$aot_result" -ne 0; then exit "$aot_result"; fi
python3 scripts/flutter/verify-aot-device.py "$aot" android-evidence

# Independent AOT evidence does not turn a failed Flutter suite into a green job.
exit "$first_failure"
