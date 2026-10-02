# Flutter native engine ABI

This is the Flutter desktop/iOS entrypoint into `internal/mobileapi` and the
shared protocol engine. Android keeps `cmd/iotools-android` and its JNI USB
transport. There is no Android/JNI dependency in this bridge, no second protocol
implementation and no browser/TCP adapter.

`iotools_native.h` is the stable host contract. All strings are UTF-8 buffers with
explicit byte lengths. Replies are C allocations with no trailing NUL and must
be copied and freed exactly once using `IotoolsNativeFree`. The ABI exposes opaque
64-bit session IDs, not Go pointers. Each session is isolated, IDs are never
reused, and capacity is reserved before opening so concurrent opens cannot exceed
16 sessions. Unknown flags, invalid UTF-8 and out-of-bounds lengths are rejected.

The trusted platform host creates/resolves its app-support directory and passes
that absolute immutable root separately from the full configuration path. Initial
opens may initialize the bundled sample; explicit recovery uses flag 4 and never
creates a missing configuration. JSON commands cannot change the trusted root.
Opening and previewing do not start network I/O; writes retain the engine's
preview/token/confirmation rules. Lifecycle cancellation is not held behind
synchronous commands, and resume never replays a request. Hosts should call
commands in a worker isolate and lifecycle/cancellation from an independent
isolate when needed; all isolates must use the same live handle.

## Build

Use the repository's pinned Go toolchain and platform C compiler, with cgo enabled:

```sh
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o libiotools_native.so ./cmd/iotools-native
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o iotools_native.dll ./cmd/iotools-native
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o libiotools_native.dylib ./cmd/iotools-native
```

These commands run on their respective Linux, Windows and macOS builders; Apple
SDK/toolchains are required for macOS/iOS. For iOS build `-buildmode=c-archive`
with `GOOS=ios`, the target SDK and architecture, then link the archive into the
Runner and retain the exported ABI symbols for `DynamicLibrary.process()`.
Device and simulator archives must target the matching SDK, including separate
arm64 device and arm64 simulator slices. A Linux build is not evidence of iOS or
macOS binary compatibility.

Bundle paths used by the Flutter host:

- Linux: executable directory `lib/libiotools_native.so`
- Windows: executable directory `iotools_native.dll`
- macOS: app bundle `Contents/Frameworks/libiotools_native.dylib`
- iOS: statically linked Runner symbols

## Verification

```sh
go test -race -count=1 ./cmd/iotools-native
python3 cmd/iotools-native/testdata/check_abi.py /absolute/path/to/libiotools_native.so
cc -Wall -Wextra -Werror -Icmd/iotools-native \
  cmd/iotools-native/testdata/header_smoke.c /absolute/path/to/libiotools_native.so \
  -Wl,-rpath,/absolute/path/to -o /tmp/iotools-header-smoke
/tmp/iotools-header-smoke /absolute/private/root/collection.yaml /absolute/private/root
```

The Python ABI check also accepts a Windows DLL or macOS dylib. It uses temporary
files and a local HTTP fixture to verify real execution, absence of startup I/O,
write confirmation, token replay rejection, UTF-8 and length handling, buffer
ownership, session isolation/capacity, recovery and stale-handle safety. The Go
tests run the registry under the race detector and verify cancellation can pass a
blocked command. The C consumer checks the checked-in header against real exports.
