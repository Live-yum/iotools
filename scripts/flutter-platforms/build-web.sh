#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root/mobile"
flutter pub get
flutter test test/platform/icon_assets_test.dart --concurrency=1 --dart-define=IOTOOLS_GENERATE_PLATFORM_ICONS=true
flutter build web --release --csp --no-web-resources-cdn --pwa-strategy=none --dart-define="IOTOOLS_SHA=${IOTOOLS_SHA:-local}"
cd "$root"
python3 scripts/flutter-platforms/prepare-web-fonts.py
python3 - <<'PY'
from pathlib import Path
import shutil
p=Path('cmd/iotools-web/web_assets')
if p.exists():shutil.rmtree(p)
shutil.copytree('mobile/build/web',p)
PY
target="${1:-$(go env GOHOSTOS)}"
arch="${2:-$(go env GOHOSTARCH)}"
goos="$target"
ext=''
if test "$target" = macos; then goos=darwin; fi
if test "$goos" = windows; then ext=.exe; fi
out="platform-dist/web-$target-$arch"
mkdir -p "$out"
GOOS="$goos" GOARCH="$arch" CGO_ENABLED=0 go build -tags flutter_web_assets -trimpath -ldflags "-s -w -X main.version=${IOTOOLS_SHA:-local}" -o "$out/iotools-web$ext" ./cmd/iotools-web
python3 scripts/flutter-platforms/package-web.py "$target" "$arch"
