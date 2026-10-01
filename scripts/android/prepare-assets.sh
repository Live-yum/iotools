#!/usr/bin/env bash
set -euo pipefail
# Native Android Canvas has no JavaScript renderer or external terminal assets.
assets=android/app/src/main/assets
mkdir -p "$assets"
cp LICENSE "$assets/LICENSE"
cp docs/android.md "$assets/android.md"
