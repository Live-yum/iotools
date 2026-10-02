import 'engine.dart';
import 'runtime_stub.dart'
    if (dart.library.io) 'runtime_native.dart'
    if (dart.library.js_interop) 'runtime_web.dart'
    as implementation;

class AppRuntime {
  const AppRuntime({required this.engine, required this.platform});
  final Engine engine;
  final PlatformServices platform;
}

/// Keeps protocol and feature widgets independent from their transport.
AppRuntime createRuntime() => implementation.createRuntime();
