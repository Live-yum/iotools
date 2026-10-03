import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/failure_reporting.dart';

void main() {
  test('disposed diagnostic tree preserves original exception, stack and failure', () {
    final original = StateError('original application failure');
    final stack = StackTrace.fromString(
      '#0      originalApplication (package:iotools_mobile/failure_fixture.dart:17:3)',
    );
    var calls = 0;
    final messages = <String>[];
    final report = preserveOriginalFailure((details, description) {
      calls++;
      expect(identical(details.exception, original), isTrue);
      expect(identical(details.stack, stack), isTrue);
      expect(description, 'failed case');
      expect(details.toString(), contains('original application failure'));
      expect(details.toString(), contains('originalApplication'));
      expect(details.toString(), contains('IOTOOLS_DIAGNOSTIC_SERIALIZATION_FAILED'));
    }, emit: messages.add);
    report(FlutterErrorDetails(
      exception: original,
      stack: stack,
      informationCollector: () => throw StateError('disposed render tree'),
    ), 'failed case');
    expect(calls, 1);
    expect(messages.single, contains('original application failure'));
    expect(messages.single, contains(stack.toString()));
  });

  test('live diagnostic text is frozen and a failing delegate still fails', () {
    var collections = 0;
    final failure = StateError('reporter failure');
    final report = preserveOriginalFailure((details, description) {
      expect(details.toString(), contains('live tree'));
      expect(details.toString(), contains('live tree'));
      throw failure;
    }, emit: (_) {});
    expect(() => report(FlutterErrorDetails(
      exception: StateError('original'),
      informationCollector: () {
        collections++;
        return [ErrorDescription('live tree')];
      },
    ), 'failed case'), throwsA(same(failure)));
    expect(collections, 1);
  });

  test('diagnostic output is bounded without changing the reported exception', () {
    final original = StateError('x' * 30000);
    final messages = <String>[];
    preserveOriginalFailure((details, _) {
      expect(identical(details.exception, original), isTrue);
    }, emit: messages.add)(FlutterErrorDetails(exception: original), 'failed case');
    expect(messages.single.length, lessThan(12500));
    expect(messages.single, contains('[truncated]'));
  });
}
