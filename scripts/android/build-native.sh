#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
ndk="${ANDROID_NDK_HOME:-${ANDROID_HOME:?ANDROID_HOME required}/ndk/28.1.13356709}"
toolchain="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin"
version="${IOTOOLS_SHA:-android-local}"
cd "$root"
for item in "arm64-v8a arm64 aarch64-linux-android" "x86_64 amd64 x86_64-linux-android"; do
 read -r abi arch triple <<< "$item"
 mkdir -p "android/app/src/main/jniLibs/$abi"
 GOOS=android GOARCH="$arch" CGO_ENABLED=1 CC="$toolchain/${triple}26-clang"   go build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$version -extldflags=-Wl,-z,max-page-size=16384"   -o "android/app/src/main/jniLibs/$abi/libiotools.so" ./cmd/iotools-android
 rm "android/app/src/main/jniLibs/$abi/libiotools.h"
 "$toolchain/llvm-readelf" -l "android/app/src/main/jniLibs/$abi/libiotools.so" | tee "android/native-$abi.txt"
done
