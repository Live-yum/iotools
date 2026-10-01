#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
target="${1:?target is required}"
arch="${2:-$(go env GOHOSTARCH)}"
if test "$target" = ios-device; then arch=arm64; fi
if test "$target" = ios-simulator; then
  case "$arch" in x86_64) arch=amd64;; arm64|amd64) ;; *) echo 'Unsupported iOS simulator architecture' >&2; exit 2;; esac
fi
build_sha="${IOTOOLS_SHA:-local}"
case "$build_sha" in *[!a-zA-Z0-9._-]*) echo 'Invalid revision' >&2; exit 2;; esac
export CGO_ENABLED=1
out="$repo_root/mobile/native/$target-$arch"
mkdir -p "$out"
case "$target" in
 linux|windows)
  export GOOS="$target" GOARCH="$arch"
  name=libiotools_native.so
  if test "$target" = windows; then name=iotools_native.dll; fi
  native_library="$out/$name"
  go build -trimpath -ldflags "-s -w -X main.version=$build_sha" -buildmode=c-shared -o "$native_library" ./cmd/iotools-native
  ;;
 macos)
  export GOOS=darwin
  export CGO_CFLAGS="-mmacosx-version-min=13.0"
  export CGO_LDFLAGS="-mmacosx-version-min=13.0"
  for apple_arch in arm64 amd64; do
   GOARCH="$apple_arch" go build -trimpath -ldflags "-s -w -X main.version=$build_sha" -buildmode=c-shared -o "$out/$apple_arch.dylib" ./cmd/iotools-native
  done
  native_library="$out/libiotools_native.dylib"
  lipo -create "$out/arm64.dylib" "$out/amd64.dylib" -output "$native_library"
  install_name_tool -id '@rpath/libiotools_native.dylib' "$native_library"
  codesign --force --sign - --timestamp=none "$native_library"
  ;;
 ios-simulator|ios-device)
  sdk=iphonesimulator
  triple="$arch-apple-ios15.0-simulator"
  if test "$arch" = amd64; then triple=x86_64-apple-ios15.0-simulator; fi
  if test "$target" = ios-device; then sdk=iphoneos; arch=arm64; triple=arm64-apple-ios15.0; fi
  sdk_root="$(xcrun --sdk "$sdk" --show-sdk-path)"
  export CC="$(xcrun --sdk "$sdk" --find clang)"
  export GOOS=ios GOARCH="$arch"
  export CGO_CFLAGS="-isysroot $sdk_root -target $triple"
  export CGO_LDFLAGS="-isysroot $sdk_root -target $triple"
  native_library="$out/libiotools_native.a"
  go build -trimpath -ldflags "-X main.version=$build_sha" -buildmode=c-archive -o "$native_library" ./cmd/iotools-native
  apple_arch=arm64
  if test "$arch" = amd64; then apple_arch=x86_64; fi
  xcrun lipo -verify_arch "$apple_arch" "$native_library"
  symbols="$(xcrun nm -g "$native_library")"
  for symbol in IotoolsNativeABIVersion IotoolsNativeOpen IotoolsNativeCommand IotoolsNativeLifecycle IotoolsNativeFree; do
   printf '%s\n' "$symbols" | grep -E " [Tt] _$symbol$" >/dev/null
  done
  mkdir -p mobile/ios/Native
  cp "$native_library" mobile/ios/Native/libiotools_native.a
  ;;
 *) echo "Unsupported native target: $target" >&2; exit 2;;
esac
if command -v cygpath >/dev/null 2>&1; then native_library="$(cygpath -m "$native_library")"; fi
if test -n "${GITHUB_ENV:-}"; then printf 'IOTOOLS_NATIVE_LIBRARY=%s\n' "$native_library" >> "$GITHUB_ENV"; fi
printf '%s\n' "$native_library"
