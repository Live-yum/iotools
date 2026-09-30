#!/usr/bin/env bash
set -euo pipefail
collect() {
 result=$?
 mkdir -p android-evidence
 timeout 20s adb pull /sdcard/Android/data/io.github.liveyum.iotools/files/screenshots android-evidence/ || true
 timeout 15s adb logcat -d > android-evidence/logcat.txt || true
 timeout 15s adb exec-out screencap -p > android-evidence/emulator-last.png || true
 printf 'API=%s target=%s test_exit=%s\n' "${ANDROID_TEST_API:-unknown}" "${ANDROID_TEST_TARGET:-unknown}" "$result" > android-evidence/platform.txt
}
trap collect EXIT
timeout 12m gradle -p android --no-daemon connectedDebugAndroidTest
