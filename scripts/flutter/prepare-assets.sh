#!/usr/bin/env bash
set -euo pipefail
assets=mobile/android/app/src/main/assets
mkdir -p "$assets"
cp LICENSE "$assets/LICENSE"
cp docs/android.md "$assets/android.md"
mkdir -p "$assets/licenses"
cp mobile/licenses/usb-serial-for-android.txt "$assets/licenses/"
# Run the collector on the host; only go list uses the shipped Android targets.
# A fresh staging directory prevents stale desktop/test notices from surviving.
notices="$(mktemp -d "$assets/.notices.XXXXXX")"
trap 'rm -rf "$notices"' EXIT
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
GOOS="$host_os" GOARCH="$host_arch" CGO_ENABLED=0 go run ./scripts/notices \
  -goos android -goarch arm64,amd64 -cgo 1 "$notices" ./cmd/iotools-android
rm -rf "$assets/THIRD_PARTY_LICENSES"
mv "$notices" "$assets/THIRD_PARTY_LICENSES"
