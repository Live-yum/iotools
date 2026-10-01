import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/core/json.dart';
import '../support/fake_engine.dart';

void main() {
  test(
    'exact JSON preserves decimal and UInt64 lexical values and duplicate fails',
    () {
      final source =
          '{"max":18446744073709551615,"decimal":1.0000000000000001,"text":"中文😀"}';
      expect(exactEncode(exactDecode(source)), source);
      expect(() => exactDecode('{"x":1,"x":2}'), throwsFormatException);
    },
  );
  test(
    'pause invalidates in-flight polls and never replays requests',
    () async {
      final engine = FakeEngine(), session = AppSession(engine, FakePlatform());
      await session.initialize();
      session.started({'run_id': 'r'});
      engine.pendingEvents = Completer();
      final future = session.poll();
      await session.pause();
      engine.pendingEvents!.complete({
        'running': true,
        'events': [
          {'run_id': 'r', 'seq': 1, 'kind': 'message', 'data': {}},
        ],
      });
      await future;
      expect(session.events, isEmpty);
      expect(session.running, isFalse);
      expect(session.status, '已取消');
      await session.resume();
      expect(engine.calls.where((e) => e['op'] == 'run'), isEmpty);
      session.dispose();
    },
  );
  test(
    'dispose ignores delayed event responses and running-only transitions notify',
    () async {
      final engine = FakeEngine(), session = AppSession(engine, FakePlatform());
      await session.initialize();
      engine.pendingEvents = Completer();
      final future = session.poll();
      session.dispose();
      engine.pendingEvents!.complete({
        'running': true,
        'events': [
          {'kind': 'message'},
        ],
      });
      await future;
      expect(engine.closed, isTrue);
    },
  );
  test(
    'Modbus source retains actual run provenance independent of draft',
    () async {
      final e = FakeEngine(), s = AppSession(e, FakePlatform());
      await s.initialize();
      await s.run(
        {
          'id': 'A',
          'protocol': 'modbus',
          'endpoint': '{{host}}',
          'action': 'read-holding',
          'params': {'unit': '{{unit}}'},
        },
        {'token': 'n'},
      );
      s.updateDraft({'id': 'B', 'endpoint': 'other'});
      e.pendingEvents = Completer()
        ..complete({
          'running': false,
          'events': [
            {
              'run_id': 'opaque-run',
              'seq': 1,
              'kind': 'started',
              'data': {
                'source': {
                  'endpoint': 'tcp://actual:502',
                  'unit': 3,
                  'action': 'read-holding',
                },
              },
            },
            {
              'run_id': 'opaque-run',
              'seq': 2,
              'kind': 'done',
              'data': {'status': 'completed'},
            },
          ],
        });
      await s.poll();
      expect(s.resultRequest!['endpoint'], 'tcp://actual:502');
      expect(mapOf(s.resultRequest!['params'])['unit'], 3);
      expect(s.originalResultRequest!['endpoint'], '{{host}}');
      expect(s.status, '已完成');
      s.dispose();
    },
  );
  test('saving form refuses to discard independently edited YAML', () async {
    final e = FakeEngine(), s = AppSession(e, FakePlatform());
    await s.initialize();
    s.source += '\n# unsaved secret';
    await expectLater(s.saveRequest({'id': 'a'}), throwsA(isA<Exception>()));
    expect(e.calls.where((c) => c['op'] == 'request.save'), isEmpty);
    expect(s.source, contains('unsaved secret'));
    s.dispose();
  });
  test(
    'lifecycle change while run command returns cannot resurrect running state',
    () async {
      final e = DelayedRunEngine(), s = AppSession(e, FakePlatform());
      await s.initialize();
      final run = s.run({'id': 'a', 'protocol': 'http'}, {'token': 'n'});
      final expectation = expectLater(run, throwsA(isA<Exception>()));
      await Future<void>.delayed(Duration.zero);
      await s.pause();
      e.pendingRun.complete({'run_id': 'late-run'});
      await expectation;
      expect(s.running, isFalse);
      expect(s.resultRunId, isEmpty);
      expect(
        e.calls.any((c) => c['op'] == 'cancel' && c['run_id'] == 'late-run'),
        isTrue,
      );
      s.dispose();
    },
  );
  test(
    'request ID rename uses original_id and discarding draft does not delete saved request',
    () async {
      final e = FakeEngine(), s = AppSession(e, FakePlatform());
      await s.initialize();
      final original = s.requests.first;
      s.choose(original);
      final changed = {...original, 'id': 'renamed'};
      s.updateDraft(changed);
      await s.saveRequest(changed);
      final command = e.calls.lastWhere((c) => c['op'] == 'request.save');
      expect(command['original_id'], 'http_demo');
      expect((command['request'] as Map)['id'], 'renamed');
      s.prepare({...original, 'name': 'unsaved'});
      s.discardDraft('http_demo');
      expect(s.drafts.containsKey('http_demo'), false);
      expect(e.calls.where((c) => c['op'] == 'request.delete'), isEmpty);
      s.dispose();
    },
  );
}

class DelayedRunEngine extends FakeEngine {
  final pendingRun = Completer<Object?>();
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'run') return pendingRun.future;
    return super.command(c);
  }
}
