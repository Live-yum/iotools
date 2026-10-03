import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

String boundedFailureText(Object? value, {int limit = 12000}) {
  String text;
  try {
    text = value?.toString() ?? '';
  } catch (error) {
    text = '[unprintable ${value.runtimeType}: ${error.runtimeType}]';
  }
  return text.length <= limit
      ? text
      : '${text.substring(0, limit)}…[truncated]';
}

/// The pinned integration binding serializes FlutterErrorDetails only after the
/// test body (including cleanup) returns. Its lazy render-tree collector can
/// then reference disposed RenderParagraphs and replace the original failure.
/// Freeze only that ancillary diagnostic text; forward the same exception and
/// stack to the real reporter exactly once, so its failure accounting survives.
TestExceptionReporter preserveOriginalFailure(
  TestExceptionReporter delegate, {
  void Function(String)? emit,
}) => (details, description) {
  final output = emit ?? (message) => debugPrintSynchronously(message);
  output('IOTOOLS_TEST_FAILURE_PRIMARY ${boundedFailureText(description)}\n'
      '${boundedFailureText(details.exception)}\n'
      '${boundedFailureText(details.stack)}');
  String diagnostics;
  try {
    diagnostics = boundedFailureText(details.toString());
  } catch (error) {
    diagnostics = 'IOTOOLS_DIAGNOSTIC_SERIALIZATION_FAILED '
        '${boundedFailureText(error, limit: 1024)}';
  }
  delegate(
    FlutterErrorDetails(
      exception: details.exception,
      stack: details.stack,
      library: details.library,
      stackFilter: details.stackFilter,
      silent: details.silent,
      informationCollector: () => [ErrorDescription(diagnostics)],
    ),
    description,
  );
};

void installOriginalFailureReporter() {
  reportTestException = preserveOriginalFailure(reportTestException);
}
