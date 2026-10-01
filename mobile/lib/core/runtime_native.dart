import 'dart:io';
import 'engine.dart';
import 'runtime.dart';
import 'platform/native/native_engine.dart';
import 'platform/native/native_platform.dart';

AppRuntime createRuntime() {
  if (Platform.isAndroid) {
    return const AppRuntime(
      engine: MethodChannelEngine(),
      platform: MethodChannelPlatform(),
    );
  }
  final platform = NativePlatformServices();
  return AppRuntime(
    engine: NativeFfiEngine(root: platform.privateRoot),
    platform: platform,
  );
}
