import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/session.dart';
import '../integration_test/http_fixture_diagnostics.dart';
import 'support/fake_engine.dart';

void main() {
  test('fixture diagnostics distinguish body read from completed response', () {
    final fixture = HTTPFixtureDiagnostics();
    for (final stage in [
      'run_requested',
      'accepted',
      'body_read',
      'close_started',
    ]) {
      fixture.record(stage, path: '/aes');
    }
    final pending = fixture.snapshot();
    expect(pending['counts']['body_read'], 1);
    expect(pending['counts']['response_closed'], 0);
    fixture.record('response_closed', path: '/aes');
    final completed = fixture.snapshot();
    expect(completed['counts']['response_closed'], 1);
    expect(pending['counts']['response_closed'], 0);
    final times = (completed['stages'] as List)
        .map((stage) => stage['elapsed_ms'] as int)
        .toList();
    expect(times, orderedEquals([...times]..sort()));
    expect(times.first, greaterThanOrEqualTo(0));
    expect(completed['elapsed_ms'], greaterThanOrEqualTo(times.last));
  });

  test('fixture metadata remains bounded and error counts stay exact', () {
    final fixture = HTTPFixtureDiagnostics();
    for (var i = 0; i < 40; i++) {
      fixture.record('errors', path: 'p' * 1000, error: 'e' * 10000);
    }
    final snapshot = fixture.snapshot();
    expect(snapshot['counts']['errors'], 40);
    expect(snapshot['stages'], hasLength(32));
    expect(snapshot['omitted_stages'], 8);
    expect(jsonEncode(snapshot).length, lessThan(40000));
    expect(snapshot['stages'].first['error'], endsWith('[truncated]'));
  });

  test('retained error and terminal event need no destructive engine poll', () {
    final engine = FakeEngine();
    final session = AppSession(engine, FakePlatform());
    session.resultRunId = 'current';
    session.status = '执行失败';
    session.error = 'context deadline exceeded';
    session.events.addAll([
      {
        'run_id': 'old',
        'kind': 'done',
        'data': {'error': 'old failure'},
      },
      {
        'run_id': 'current',
        'kind': 'response',
        'data': {'body': 'not logged'},
      },
      {
        'run_id': 'current',
        'kind': 'done',
        'data': {
          'status': 'failed',
          'error': session.error,
          'request_id': 'aes',
        },
      },
    ]);
    final snapshot = retainedHTTPDiagnostics(session);
    expect(snapshot['error'], 'context deadline exceeded');
    expect(snapshot['event_kinds'], ['response', 'done']);
    expect(jsonDecode(snapshot['terminal_event'])['data']['request_id'], 'aes');
    expect(jsonEncode(snapshot), isNot(contains('old failure')));
    expect(jsonEncode(snapshot), isNot(contains('not logged')));
    expect(engine.calls, isEmpty);
    expect(session.events, hasLength(3));
  });

  test('retained diagnostics tolerate no done event and bound long errors', () {
    final session = AppSession(FakeEngine(), FakePlatform());
    expect(retainedHTTPDiagnostics(session)['terminal_event'], isNull);
    session.resultRunId = 'current';
    session.error = 'x' * 20000;
    session.events.add({
      'run_id': 'current',
      'kind': 'done',
      'data': {'status': 'failed', 'error': session.error},
    });
    final snapshot = retainedHTTPDiagnostics(session);
    expect(snapshot['error'], endsWith('[truncated]'));
    expect(snapshot['terminal_event'], endsWith('[truncated]'));
    expect(jsonEncode(snapshot).length, lessThan(7000));
  });
}
