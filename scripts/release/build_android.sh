#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${IOTOOLS_SHA:?Exact source SHA required}"
: "${ANDROID_HOME:?Android SDK required}"
: "${FLUTTER_ROOT:?Pinned Flutter required}"
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/28.1.13356709"
mkdir -p android-evidence release-android
(
 cd mobile
 flutter pub get --enforce-lockfile
 flutter pub deps --json > ../android-evidence/pub-dependencies.json
 flutter analyze
 flutter test --concurrency=1
 printf 'sdk.dir=%s\nflutter.sdk=%s\n' "$ANDROID_HOME" "$FLUTTER_ROOT" > android/local.properties
 gradle -p android wrapper --gradle-version 8.11.1
)
# The original build initializes Gradle/SDK before requiring the pinned NDK.
printf 'Pinned NDK: %s\n' "$ANDROID_NDK_HOME"
if ! test -d "$ANDROID_NDK_HOME"; then
 find "$ANDROID_HOME/ndk" -maxdepth 2 -name source.properties -print 2>/dev/null || true
 echo "Required original NDK 28.1.13356709 is unavailable after Gradle initialization" >&2
 exit 1
fi
cat "$ANDROID_NDK_HOME/source.properties"
python3 scripts/flutter/android_dependency_audit.py --deps android-evidence/pub-dependencies.json --output android-evidence/dependency-resolution.json
python3 scripts/flutter/test_android_dependency_audit.py
python3 scripts/flutter/test_release_apk_gate.py
python3 scripts/flutter/verify-mobile.py
bash scripts/android/build-native.sh
bash scripts/flutter/prepare-assets.sh
(
 cd mobile
 # Keep the historically accepted universal normal-entry build command exactly.
 flutter build apk --release --target-platform android-arm64,android-x64 --target lib/main.dart
 cd android
 ./gradlew --no-daemon :app:dependencies --configuration releaseRuntimeClasspath > ../../android-evidence/release-runtime-dependencies.txt
)
# Preserve the normal-entry universal APK before building the independent ARM64 package.
cp mobile/build/app/outputs/flutter-apk/app-release.apk release-android/universal.apk
(
 cd mobile
 flutter build apk --release --split-per-abi --target-platform android-arm64 --target lib/main.dart
)
cp mobile/build/app/outputs/flutter-apk/app-arm64-v8a-release.apk release-android/arm64-v8a.apk
tools=$(find "$ANDROID_HOME/build-tools" -mindepth 1 -maxdepth 1 -type d | sort -V | tail -1)
for abi in arm64-v8a universal; do
 apk="release-android/$abi.apk"
 flag=--aot-arm64; key=android-arm64-v8a-aot-test-signed
 if test "$abi" = universal; then flag=--aot; key=android-universal-arm64-x86_64-aot-test-signed; fi
 python3 scripts/flutter/verify-mobile.py "$apk" "$flag"
 python3 scripts/flutter/android_dependency_audit.py --deps android-evidence/pub-dependencies.json --maven android-evidence/release-runtime-dependencies.txt --apk "$apk" --output "android-evidence/$abi-dependency-packaging.json"
 "$tools/apksigner" verify --verbose --print-certs "$apk" > "android-evidence/$abi-signature.txt"
 "$tools/aapt" dump permissions "$apk" > "android-evidence/$abi-permissions.txt"
 python3 scripts/flutter/check-apk-permissions.py "android-evidence/$abi-permissions.txt"
 "$tools/zipalign" -c -P 16 -v 4 "$apk" > "android-evidence/$abi-zipalign.txt"
 python3 scripts/release/android_report.py "$abi" "$apk"
 python3 scripts/release/release.py stage --sha "$IOTOOLS_SHA" --key "$key" --asset "$apk" --report "release-android/$abi.json"
done
