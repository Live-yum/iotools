// Invoked explicitly by CI. This file intentionally fails and is not a normal
// *_test.dart suite. CI requires a nonzero exit and the original error/stack.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../../integration_test/failure_reporting.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  installOriginalFailureReporter();
  testWidgets('intentional original failure survives disposed diagnostics', (t) async {
    await t.pumpWidget(const MaterialApp(home: Text('live tree')));
    FlutterError.reportError(FlutterErrorDetails(
      exception: StateError('IOTOOLS_INTENTIONAL_ORIGINAL_FAILURE'),
      stack: StackTrace.fromString('IOTOOLS_ORIGINAL_STACK_MARKER'),
      informationCollector: () => throw StateError('disposed render tree'),
    ));
    await t.pumpWidget(const SizedBox());
  });
}
