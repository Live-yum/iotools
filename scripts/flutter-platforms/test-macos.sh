#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root/mobile"
case "${1:?architecture is required}" in
 arm64) apple_arch=arm64;; amd64) apple_arch=x86_64;; *) exit 2;;
esac
# Flutter's documented Xcode-setting environment bridge comes after its CI
# defaults. Explicitly retain the repository's normal sandboxed Debug profile.
# No entitlement or system security setting is weakened for this test.
export FLUTTER_XCODE_CODE_SIGN_ENTITLEMENTS="$root/mobile/macos/Runner/DebugProfile.entitlements"
export FLUTTER_XCODE_CODE_SIGN_IDENTITY=-
export FLUTTER_XCODE_CODE_SIGN_STYLE=Manual
export FLUTTER_XCODE_DEVELOPMENT_TEAM=
export FLUTTER_XCODE_ARCHS="$apple_arch"
export FLUTTER_XCODE_ONLY_ACTIVE_ARCH=YES
flutter drive --driver=test_driver/native_driver.dart \
 --target=integration_test/native_smoke_test.dart -d macos \
 --dart-define="IOTOOLS_SHA=${IOTOOLS_SHA:-local}"
app="$root/mobile/build/macos/Build/Products/Debug/iotools.app"
codesign --verify --deep --strict "$app"
mkdir -p "$root/platform-evidence"
codesign -d --entitlements :- "$app" > "$root/platform-evidence/macos-test-entitlements.plist"
python3 - "$root/platform-evidence/macos-test-entitlements.plist" <<'PY'
import plistlib,sys
from pathlib import Path
e=plistlib.loads(Path(sys.argv[1]).read_bytes())
assert e.get('com.apple.security.app-sandbox') is True, e
assert e.get('com.apple.security.network.client') is True, e
assert e.get('com.apple.security.network.server') is True, e
assert not e.get('com.apple.security.cs.disable-library-validation',False), e
print('PASS actual Flutter/Go HTTP UI app retained App Sandbox and normal network entitlements')
PY
