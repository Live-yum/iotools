#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root/android"
npm ci --ignore-scripts --no-audit --no-fund
dest=app/src/main/assets/vendor
mkdir -p "$dest"
cp node_modules/@xterm/xterm/lib/xterm.js "$dest/"
cp node_modules/@xterm/xterm/css/xterm.css "$dest/"
cp node_modules/@xterm/addon-fit/lib/addon-fit.js "$dest/"
cp node_modules/@xterm/xterm/LICENSE "$dest/xterm-LICENSE"
cp node_modules/@xterm/addon-fit/LICENSE "$dest/addon-fit-LICENSE"
cp ../LICENSE app/src/main/assets/LICENSE
mkdir -p app/src/main/assets/docs
cp ../docs/*.md app/src/main/assets/docs/
