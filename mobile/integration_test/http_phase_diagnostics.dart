import 'package:flutter/foundation.dart';

// Closed vocabulary: never accept configuration data, screenshot names, paths,
// errors, credentials, or arbitrary labels.
enum HTTPTestPhase {
  testBodyStart,
  runtimeReady,
  serverBindBegin,
  serverBound,
  settingsSaveBegin,
  settingsSaved,
  nativeCloseBeforeOpenBegin,
  nativeClosedBeforeOpen,
  nativeOpenBegin,
  nativeOpened,
  configGetBegin,
  configRead,
  configSaveBegin,
  configSaved,
  nativeCloseBeforeAppBegin,
  nativeClosedBeforeApp,
  appPumpBegin,
  appPumped,
  appReady,
  screenshotBegin,
  surfaceConversionBegin,
  surfaceConversionEnd,
  screenshotEnd,
  requestConfirmationBegin,
  requestConfirmed,
  httpCompleted,
}

class HTTPPhaseDiagnostics {
  HTTPPhaseDiagnostics({
    bool enabled = const bool.fromEnvironment('IOTOOLS_TEST_FIXTURES'),
    void Function(String)? emit,
  }) : _enabled = enabled,
       _emit = emit ?? debugPrintSynchronously;

  final bool _enabled;
  final void Function(String) _emit;
  final Stopwatch _elapsed = Stopwatch()..start();
  int _sequence = 0;

  void mark(HTTPTestPhase phase) {
    if (!_enabled) return;
    // Do not wait for the throttled debugPrint queue during a native crash.
    // Only fixed tokens and integers are emitted.
    _emit(
      'IOTOOLS_HTTP_PHASE stage=${phase.name} sequence=${++_sequence} '
      'elapsed_ms=${_elapsed.elapsedMilliseconds}',
    );
  }
}
