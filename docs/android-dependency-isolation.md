# Android path-provider dependency isolation

This is a controlled dependency experiment, not a demonstrated crash fix. The
prior Android run [36933124951](https://github.com/Live-yum/iotools/actions/runs/36933124951)
failed the business-driver, OPC UA completion and normal-entry AOT acceptance
gates. No passing result can be inferred from removing an unused dependency.

## One intended runtime change

Pin the official endorsed `path_provider_android` implementation to **2.2.23**
through a normal direct dependency (no dependency override). Keep `path_provider`
2.1.5 and its compatible platform-interface 2.1.2. The official package's
[release history](https://pub.dev/packages/path_provider_android/changelog)
places the JNI implementation change at 2.3.0; 2.2.23 uses the supported Java /
Pigeon implementation and requires Flutter 3.35 / Dart 3.9. The repository's
Flutter 3.35.7 / Dart 3.9 toolchain satisfies those requirements. The resolver
lowers the lockfile Flutter minimum from 3.35.6 to 3.35.0 as a consequence of
removing JNI; the actual pinned SDK is unchanged.

`mobile/lib/core/runtime_native.dart` selects the existing `MethodChannelEngine`
and `MethodChannelPlatform` on Android. The path-provider feature calls live in
`NativePlatformServices`, selected on other native platforms. Flutter still
registers endorsed Android plugins at startup, even without feature calls. The
pin removes the external Dart JNI closure from that registration path without
editing generated registrants, SDK code, package sources or the APK after build.
The app's own JNI-backed Go engine remains mandatory. `file_selector_android`
remains unchanged at 0.5.2+6.

## Fail-closed comparison

`scripts/flutter/android_dependency_baseline.json` records the source lockfile
and release Maven coordinates from the prior run, including the artifact URL,
lock Git blob and dependency-report hash. The audit requires:

- Exactly path-provider Android 2.3.1 → 2.2.23
- Only the unused `jni`, `jni_flutter`, `jni_util`, `args`, `package_config`
  closure removed, with every other locked version unchanged
- The actual resolver graph equal to the generated lockfile, with the official
  Java path-provider and file-selector plugins retained
- The resolved external Maven coordinates unchanged
- No external Dart JNI native library or Java classes in the built normal AOT
  APK, while the app's own `libiotools.so` remains present

The Android workflow enforces the committed lockfile and retains JSON evidence
for both dependency resolution and APK packaging. These are static checks;
unchanged mandatory emulator business tests, OPC completion checks and normal
entry AOT tests must still pass on the exact candidate commit. Graphics backend,
renderer, system-image selection, emulator resources and time budgets are not
changed by this experiment. A failure must be reported as a failure rather than
attributed to JNI, graphics, emulator initialization or memory without evidence.
