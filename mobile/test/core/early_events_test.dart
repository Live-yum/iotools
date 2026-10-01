import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import '../support/fake_engine.dart';

class EarlyEngine extends FakeEngine {
  final runReply = Completer<Object?>();
  final batch = <JsonMap>[];
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'run') {
      calls.add(c);
      return runReply.future;
    }
    if (c['op'] == 'events') {
      final rows = [...batch];
      batch.clear();
      return {'running': false, 'events': rows};
    }
    return super.command(c);
  }
}

JsonMap event(String run, String kind, [JsonMap data = const {}]) => {
  'run_id': run,
  'kind': kind,
  'data': data,
};
void main() {
  test(
    'early real run reply restores exact results and terminal without re-broadcast',
    () async {
      final engine = EarlyEngine();
      final s = AppSession(engine, FakePlatform())..ready = true;
      final broadcast = <JsonMap>[];
      final sub = s.eventStream.listen(broadcast.add);
      final future = s.run(
        {
          'protocol': 'modbus',
          'action': 'read-holding',
          'endpoint': 'template',
          'params': {},
        },
        {'token': 'approved'},
        confirmed: true,
      );
      engine.batch.addAll([
        event('stale', 'done', {'status': 'failed', 'error': 'old'}),
        event('new', 'started', {
          'source': {
            'action': 'read-holding',
            'endpoint': 'tcp://127.0.0.1:502',
            'unit': 1,
          },
        }),
        event('new', 'registers', {
          'values': [const ExactNumber('18446744073709551615')],
        }),
        event('new', 'done', {'status': 'completed'}),
      ]);
      await s.poll();
      expect(s.events, isEmpty);
      engine.runReply.complete({'run_id': 'new'});
      await future;
      expect(s.events.map((e) => e['kind']), ['started', 'registers', 'done']);
      expect((mapOf(s.events[1]['data'])['values'] as List).single.toString(), '18446744073709551615');
      expect(s.status, '已完成');
      expect(s.running, isFalse);
      expect(s.error, isNull);
      expect(s.resultRequest!['endpoint'], 'tcp://127.0.0.1:502');
      expect(broadcast.length, 4);
      expect(engine.calls.where((c) => c['op'] == 'run').length, 1);
      await sub.cancel();
      s.dispose();
    },
  );
  test(
    'pause clears early frames and late reply cannot restart a run',
    () async {
      final engine = EarlyEngine();
      final session = AppSession(engine, FakePlatform())..ready = true;
      engine.batch.add(event('late', 'done', {'status': 'completed'}));
      await session.poll();
      await session.pause();
      await session.resume();
      session.started({'run_id': 'late'});
      expect(session.events, isEmpty);
      expect(session.status, '执行中');
      expect(engine.calls.where((c) => c['op'] == 'run'), isEmpty);
      session.dispose();
    },
  );
  test('unclaimed events are bounded and cannot cross run identity', () async {
    final engine = EarlyEngine();
    final session = AppSession(engine, FakePlatform())..ready = true;
    for (var i = 0; i < 160; i++) {
      engine.batch.add(event('old-$i', 'message', {'body': 'x' * 40000}));
    }
    engine.batch.add(
      event('current', 'done', {
        'status': 'failed',
        'error': 'current failure',
      }),
    );
    await session.poll();
    expect(session.dropped, greaterThan(0));
    session.started({'run_id': 'current'});
    expect(session.events.length, 1);
    expect(session.status, '执行失败');
    expect(session.error, 'current failure');
    session.started({'run_id': 'other'});
    expect(session.events, isEmpty);
    expect(session.error, isNull);
    session.dispose();
  });
}
