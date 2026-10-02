#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
ndk="${ANDROID_NDK_HOME:-${ANDROID_HOME:?ANDROID_HOME required}/ndk/28.1.13356709}"
toolchain="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin"
version="${IOTOOLS_SHA:-android-local}"
output="${IOTOOLS_ANDROID_DIR:-mobile/android}"
cd "$root"
for item in "arm64-v8a arm64 aarch64-linux-android" "x86_64 amd64 x86_64-linux-android"; do
 read -r abi arch triple <<< "$item"
 mkdir -p "$output/app/src/main/jniLibs/$abi"
 GOOS=android GOARCH="$arch" CGO_ENABLED=1 CC="$toolchain/${triple}26-clang" go list -deps ./cmd/iotools-android > "$output/dependencies-$abi.txt"
 if grep -E '(^github.com/rivo/tview|^github.com/gdamore/tcell|iotools/internal/tui|iotools/internal/mobile$|iotools/scripts/android-fixtures|^github.com/twmb/franz-go/pkg/kfake|^github.com/gopcua/opcua/server|^github.com/mochi-mqtt/server|^modernc.org/(libc|sqlite))' "$output/dependencies-$abi.txt"; then
  echo "Android dependency graph contains a removed desktop UI, test-server, or Android-incompatible SQLite dependency" >&2; exit 1
 fi
 grep -qx 'github.com/mattn/go-sqlite3' "$output/dependencies-$abi.txt" || { echo "Android Bionic SQLite driver missing" >&2; exit 1; }
 GOOS=android GOARCH="$arch" CGO_ENABLED=1 CC="$toolchain/${triple}26-clang"   go build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$version -extldflags=-Wl,-z,max-page-size=16384"   -o "$output/app/src/main/jniLibs/$abi/libiotools.so" ./cmd/iotools-android
 rm "$output/app/src/main/jniLibs/$abi/libiotools.h"
 "$toolchain/llvm-readelf" -l "$output/app/src/main/jniLibs/$abi/libiotools.so" | tee "$output/native-$abi.txt"
done
