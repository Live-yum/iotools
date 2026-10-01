#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root/mobile"
: "${IOTOOLS_NATIVE_LIBRARY:?build the native core first}"
# Flutter's normal CI builder disables sandboxing. Generate configuration only,
# then explicitly keep our original sandbox entitlements in the final app.
flutter build macos --release --config-only --dart-define="IOTOOLS_SHA=${IOTOOLS_SHA:-local}"
xcodebuild -workspace macos/Runner.xcworkspace -scheme Runner -configuration Release \
 -derivedDataPath build/macos -destination 'generic/platform=macOS' \
 CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= \
 CODE_SIGN_ENTITLEMENTS="$repo_root/mobile/macos/Runner/Release.entitlements" \
 IOTOOLS_NATIVE_LIBRARY="$IOTOOLS_NATIVE_LIBRARY" \
 SYMROOT="$repo_root/mobile/build/macos/Build/Products" COMPILER_INDEX_STORE_ENABLE=NO
app="$repo_root/mobile/build/macos/Build/Products/Release/iotools.app"
test -d "$app"
codesign --verify --deep --strict "$app"
codesign -d --entitlements :- "$app" > build/macos-entitlements.plist
python3 - <<'PY'
import plistlib
from pathlib import Path
p=plistlib.loads(Path('build/macos-entitlements.plist').read_bytes())
assert p.get('com.apple.security.app-sandbox') is True
assert p.get('com.apple.security.network.client') is True
assert p.get('com.apple.security.files.user-selected.read-write') is True
assert not p.get('com.apple.security.cs.disable-library-validation',False)
PY
