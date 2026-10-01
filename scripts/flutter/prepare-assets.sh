#!/usr/bin/env bash
set -euo pipefail
assets=mobile/android/app/src/main/assets
mkdir -p "$assets"
cp LICENSE "$assets/LICENSE"
cp docs/android.md "$assets/android.md"
mkdir -p "$assets/licenses"
cp mobile/licenses/usb-serial-for-android.txt "$assets/licenses/"
go run ./scripts/notices "$assets/THIRD_PARTY_LICENSES" ./internal/mobileapi golang.org/x/crypto/x509roots/fallback
