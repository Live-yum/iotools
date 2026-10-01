#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
mkdir -p android-evidence/flutter-screenshots
fixture_pid=
collect() {
 result=$?
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
fixture="${RUNNER_TEMP:-/tmp}/iotools-flutter-fixtures"
go build -o "$fixture" ./scripts/android-fixtures
"$fixture" -ready "$PWD/android-evidence/fixture-ready.json" > android-evidence/fixture.log 2>&1 &
fixture_pid=$!
for n in $(seq 1 50); do test ! -s android-evidence/fixture-ready.json || break; kill -0 "$fixture_pid"; sleep .2; done
test -s android-evidence/fixture-ready.json
for port in 48410 48411 48412 48413 48414 48415 48416; do adb reverse "tcp:$port" "tcp:$port"; done
# Real Android document provider and bounded byte-stream tests.
(cd mobile/android && timeout --kill-after=30s 5m ./gradlew --no-daemon connectedDebugAndroidTest)
# All protocol UI interactions run inside the Flutter app, backed by real local fixtures.
(cd mobile && timeout --kill-after=30s 20m flutter drive --driver=test_driver/integration_test.dart --target=integration_test/app_test.dart --no-pub --dart-define=IOTOOLS_TEST_FIXTURES=true)
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
set -e
cat android-evidence/aot-instrumentation.txt
mkdir -p android-evidence/aot-device
adb pull /sdcard/Android/data/io.github.liveyum.iotools.test/files/aot-evidence/. android-evidence/aot-device/
test "$aot_result" -eq 0
python3 scripts/flutter/verify-aot-device.py "$aot" android-evidence
