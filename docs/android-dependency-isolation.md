# Android path-provider dependency review

## Current reviewed upgrade

The integrated dependency update uses `path_provider` 2.1.6 and pins the official
endorsed Android implementation to `path_provider_android` **2.3.1**. This
deliberately replaces the earlier 2.2.23 isolation experiment described below.
It is an actual dependency upgrade, not a claim that removing or restoring JNI
fixes a crash.

`path_provider` 2.1.6 requires Flutter >=3.38 / Dart >=3.10. The repository pins
official Flutter **3.44.9 / Dart 3.12.2**, also required by `cupertino_icons` 2.0.0.
All Flutter workflows, release configuration and the committed lockfile must
agree. CI runs `flutter pub get --enforce-lockfile`.

Android feature calls still use our `MethodChannelEngine` and
`MethodChannelPlatform`; other native platforms use `NativePlatformServices`.
Nevertheless, Flutter automatically registers endorsed plugins at startup.
The 2.3.1 implementation registers a Dart `PathProviderAndroid`, while official
`jni` and `jni_flutter` provide the native Android plugins and `libdartjni.so`.
This startup path is part of the reviewed change even without path-provider
feature calls. `jni` also declares Linux/Windows FFI plugins, so those platform
build and runtime checks remain relevant.

The restored closure is exactly `args`, `jni`, `jni_flutter`, `jni_util` and
`package_config`. Full lock entries, including versions and official archive
hashes, are recorded in `android_dependency_updates.json`. The retracted
`jni_flutter` 1.0.4 is not selected; its 1.0.4+1 replacement is used. That patch's
Android source is unchanged. Neither JNI Android project adds external Maven
implementation dependencies; the effective Maven graph is still checked exactly
against the historical evidence plus reviewed Flutter engine/ExifInterface/
jspecify updates.

The upstream JNI Gradle project defaults to the SDK's NDK 28.2.13676358. Our root
Android Gradle configuration uses the supported AGP `finalizeDsl` callback to
set that project to **28.1.13356709**, matching the existing app, native Go build
and validated NDK provisioning. It does not edit downloaded package sources,
generated registrants or SDK code. This configuration must also pass real CI.

## Fail-closed checks

The original `android_dependency_baseline.json` and its source/evidence hashes
remain unchanged. A separate reviewed update record explicitly identifies the
2.2.23 -> 2.3.1 migration and restored closure. The audit now requires:

- Every locked package, version, source, archive hash and dependency kind to
  match the original evidence plus exact reviewed lock-entry updates. Unexpected
  additions, removals and unrelated drift fail.
- The resolver package/version graph to equal the lockfile, with exact direct
  dependencies for path-provider Android and all three JNI packages.
- Exactly the Android plugins `file_selector_android`, `integration_test`,
  `path_provider_android`, `jni` and `jni_flutter`. Path-provider is Dart-only;
  the other four are native. Critical plugin dependency edges are checked too.
- The resolved external Maven coordinates to equal the reviewed set, with no
  wildcard acceptance of new dependencies.
- Exactly one `libdartjni.so` for each shipped `libiotools.so` ABI, no alternate
  JNI native path/ABI, valid ELF64 architecture, and 16 KiB-compatible load
  segments. The application's own native engine remains mandatory.
- Actual DEX class definitions for both JNI plugin classes and the kept
  `JniUtils`, `PortCleaner`, `PortContinuation`, and `PortProxyBuilder` classes.
  String references to missing classes do not satisfy this check.

Static dependency/APK checks are prerequisites, not runtime acceptance. Existing
business-driver, OPC UA completion, normal-entry Release AOT startup, lifecycle,
desktop and native-platform gates must pass on the final combined source. App
crashes, missing JNI libraries/classes, unsupported toolchains and failed
business assertions must be reported as failures. No assertion, timeout,
renderer, system image or emulator resource is relaxed for this upgrade.

## Historical isolation experiment

The earlier experiment pinned `path_provider_android` 2.2.23, retained
`path_provider` 2.1.5, and removed the unused external JNI closure. Flutter
3.35.7 / Dart 3.9 was used initially, followed by the separately reviewed SDK
upgrade for Cupertino icons. The app's own JNI-backed Go engine was always
required. `file_selector_android` remained 0.5.2+6.

The prior Android run
[36933124951](https://github.com/Live-yum/iotools/actions/runs/36933124951)
failed business-driver, OPC UA completion and normal-entry AOT gates. It did
not demonstrate that external JNI caused those failures. Its dependency and
Maven evidence is preserved as provenance, not promoted to passing runtime
evidence for the current upgrade.

## Official sources

- [Flutter 3.44.9](https://github.com/flutter/flutter/tree/6b182d2c7585eba26d4edce0f97630effd256c33)
- [path_provider 2.1.6 source](https://github.com/flutter/packages/blob/path_provider-v2.1.6/packages/path_provider/path_provider/pubspec.yaml)
- [path_provider_android 2.3.1 source](https://github.com/flutter/packages/blob/path_provider_android-v2.3.1/packages/path_provider/path_provider_android/pubspec.yaml)
- [JNI 1.1.0](https://pub.dev/packages/jni/versions/1.1.0)
- [JNI Flutter versions and retraction](https://pub.dev/packages/jni_flutter/versions)
- [AGP 8.9 finalizeDsl API](https://developer.android.com/reference/tools/gradle-api/8.9/com/android/build/api/variant/AndroidComponentsExtension)
- [Android 16 KiB page-size requirements](https://developer.android.com/guide/practices/page-sizes)
