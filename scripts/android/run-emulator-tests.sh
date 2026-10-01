#!/usr/bin/env bash
set -euo pipefail
collect() {
 result=$?
 mkdir -p android-evidence
 timeout 20s adb pull /data/local/tmp/iotools-screenshots android-evidence/ || true
 timeout 15s adb logcat -d -t 6000 > android-evidence/logcat.txt || true
 grep -E 'Uncaught (SyntaxError|ReferenceError|TypeError)|net::ERR_BLOCKED|chromium.*(SyntaxError|ReferenceError|TypeError)' android-evidence/logcat.txt | tail -30 || true
 timeout 15s adb exec-out screencap -p > android-evidence/emulator-last.png || true
 printf 'API=%s target=%s test_exit=%s\n' "${ANDROID_TEST_API:-unknown}" "${ANDROID_TEST_TARGET:-unknown}" "$result" > android-evidence/platform.txt
}
trap collect EXIT
# Disposable software emulator only: reduce raster work without hiding ANRs.
adb shell wm density 160
timeout --kill-after=30s 12m gradle -p android --no-daemon connectedDebugAndroidTest
