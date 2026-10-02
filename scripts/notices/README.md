# Dependency notice collection

`go run ./scripts/notices OUTPUT_DIR [PACKAGE ...]` defaults to the desktop
`./cmd/iotools` dependency graph. Use a dedicated, initially empty output directory.
The collector uses `go list -deps`, without `-test`, and preserves license,
copying, notice, author, copyright, and patent-grant files in each imported package
directory and its ancestors up to the dependency's module root. It does not walk
unimported examples, tests, or testdata. The Go toolchain's corresponding ancestor
notices, including its vendored dependencies, are included too.

Android asset preparation executes the collector with the host's GOOS/GOARCH and
CGO disabled, then supplies `-goos android -goarch arm64,amd64 -cgo 1` to select
the union of the two shipped native targets for `./cmd/iotools-android`. Those
flags affect the collector's `go list` subprocess only; they do not cross-compile
the collector or compile the application. A fresh staging directory replaces the
previous generated Android notices, so desktop-only dependencies cannot survive
from an earlier collection. The build's existing GOFLAGS still apply to go list.

`INDEX.txt` records module identity, source-relative filename, and packaged
filename. `MANIFEST.json` records Go version, target environments, entry packages,
and the SHA-256 of every packaged notice. Entries and filenames are deterministic
for the same source tree, target selection, and toolchain. Upstream texts remain
unaltered, even when a project-wide license also describes optional subpackages.

The generated `sqlite3-binding.c.header-notice.txt` entry is an explicit exception
to whole-file copying: it contains the complete amalgamation version comment and
complete `sqliteInt.h` public-domain comment from the selected go-sqlite3 module's
`sqlite3-binding.c`. It is included only when that C amalgamation is selected and
the build is not using `USE_LIBSQLITE3`; a changed or missing dedication fails the
collection for review. The Go wrapper's separate LICENSE is also preserved.

This Go inventory is one part of APK notice packaging. The app's Apache license,
USB serial library notice, Flutter-generated NOTICES, and Android/JVM dependency
notices have their own sources. This collector does not resolve Gradle artifacts
or replace a dependency-level review when libraries or embedded sources change.

Run the focused checks with `go test ./scripts/notices`.
