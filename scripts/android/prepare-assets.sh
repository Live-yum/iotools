#!/usr/bin/env bash
set -euo pipefail
# Package local Chinese help and license notices.
assets=android/app/src/main/assets
mkdir -p "$assets"
# Generated legacy help must not leak into incremental builds.
rm -rf "$assets/docs" "$assets/vendor"
cp LICENSE "$assets/LICENSE"
cp docs/android.md "$assets/android.md"
