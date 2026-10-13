import 'package:flutter/foundation.dart';

// Closed vocabulary: never accept configuration data, screenshot names, paths,
// errors, credentials, or arbitrary labels.
enum OPCTestPhase {
  testBodyStart,
  runtimeReady,
  fixtureReadyBegin,
  fixtureReady,
  initialMetricsBegin,
  initialMetricsRead,
  nativeOpenBegin,
  nativeOpened,
  configGetBegin,
  configRead,
  configSaveBegin,
  configSaved,
  nativeCloseBegin,
  nativeClosed,
  appPumpBegin,
  appPumped,
  appReady,
  workspaceOpenBegin,
  workspaceOpened,
  discoveryBegin,
  discoveryCompleted,
  screenshotBegin,
  surfaceConversionBegin,
  surfaceConverted,
  screenshotEnd,
  postSubscriptionsAttributesBegin,
  postSubscriptionsAttributesOpened,
  postSubscriptionsRefreshBegin,
  postSubscriptionsRefreshed,
  postSubscriptionsIdleBegin,
  postSubscriptionsIdle,
  postSubscriptionsNotified,
  subscriptionsListRead,
  subscriptionsTabOpened,
  subscriptionsRefreshed,
  subscriptionStopBegin,
  subscriptionStopped,
  remainingSubscriptionVerified,
  resizeNavigationBegin,
  resizeNavigationComplete,
  surfaceResizeBegin,
  surfaceResized,
  resizeCountersVerified,
  surfaceRestored,
  backgroundStopBegin,
  backgroundStopped,
  resumeBegin,
  resumed,
  cleanupBegin,
  cleanupEnd,
}

class OPCPhaseDiagnostics {
  OPCPhaseDiagnostics({
    bool enabled = const bool.fromEnvironment('IOTOOLS_TEST_FIXTURES'),
    void Function(String)? emit,
  }) : _enabled = enabled,
       _emit = emit ?? debugPrintSynchronously;

  final bool _enabled;
  final void Function(String) _emit;
  final Stopwatch _elapsed = Stopwatch()..start();
  int _sequence = 0;

  void mark(OPCTestPhase phase) {
    if (!_enabled) return;
    // Do not wait for the throttled debugPrint queue during a native crash.
    // Only fixed tokens and integers are emitted.
    _emit(
      'IOTOOLS_OPC_PHASE stage=${phase.name} sequence=${++_sequence} '
      'elapsed_ms=${_elapsed.elapsedMilliseconds}',
    );
  }
}
