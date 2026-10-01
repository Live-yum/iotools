import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import '../../integration_test/modbus_interaction.dart';
import '../support/fake_engine.dart';

JsonMap started(String run, [String action = 'write-typed']) => {
  'run_id': run,
  'kind': 'started',
  'data': {
    'source': {'action': action},
  },
};
JsonMap done(String run, [String status = 'completed', String error = '']) => {
  'run_id': run,
  'kind': 'done',
  'data': {'status': status, 'error': error},
};

class OperationClock {
  DateTime now = DateTime.utc(2026);
  int ticks = 0;
  Future<void> Function(int)? onTick;
  Future<void> advance(Duration duration) async {
    now = now.add(duration);
    ticks++;
    await onTick?.call(ticks);
  }

  Future<String> wait(
    ModbusOperationWait operation, [
    String action = 'write-typed',
  ]) => operation.waitForOperation(
    action: action,
    clock: () => now,
    advance: advance,
  );
}

class QueuedOperationEngine extends FakeEngine {
  final pending = <JsonMap>[];
  @override
  Future<Object?> command(JsonMap command) async {
    if (command['op'] == 'events') {
      final rows = [...pending];
      pending.clear();
      return {'events': rows, 'running': false};
    }
    return super.command(command);
  }
}

void main() {
  test(
    'previous idle terminal cannot satisfy a newly confirmed operation',
    () async {
      final engine = FakeEngine();
      final session = AppSession(engine, FakePlatform());
      session.resultRunId = 'old';
      session.events.addAll([started('old'), done('old')]);
      final operation = ModbusOperationWait(session), clock = OperationClock();
      clock.onTick = (tick) async {
        if (tick == 6) {
          session.started({'run_id': 'new'});
          session.events.add(started('new'));
        }
        if (tick == 9) {
          session.events.add(done('new'));
          session.state['running'] = false;
        }
      };
      expect(await clock.wait(operation), 'new');
      expect(clock.ticks, 9);
      expect(engine.calls, isEmpty);
      await operation.dispose();
      session.dispose();
    },
  );
  test(
    'fast events before run reply remain in actual session and broadcast once',
    () async {
      final engine = QueuedOperationEngine();
      final session = AppSession(engine, FakePlatform())..ready = true;
      session.resultRunId = 'old';
      session.events.add(done('old'));
      final operation = ModbusOperationWait(session), clock = OperationClock();
      clock.onTick = (tick) async {
        if (tick == 3) {
          engine.pending.addAll([started('fast'), done('fast')]);
          await session.poll();
          session.started({'run_id': 'fast'});
        }
      };
      expect(await clock.wait(operation), 'fast');
      expect(session.events.map((e) => e['kind']), ['started', 'done']);
      expect(session.status, '已完成');
      expect(session.running, isFalse);
      expect(operation.observed.length, 2);
      await operation.dispose();
      session.dispose();
    },
  );
  test(
    'terminal failure and wrong action cannot satisfy the expected write',
    () async {
      for (final wrong in [false, true]) {
        final session = AppSession(FakeEngine(), FakePlatform());
        final operation = ModbusOperationWait(session),
            clock = OperationClock();
        session.started({'run_id': 'bad'});
        session.events.addAll([
          started('bad', wrong ? 'read-holding' : 'write-typed'),
          done('bad', wrong ? 'completed' : 'failed', 'fixture rejected'),
        ]);
        await expectLater(clock.wait(operation), throwsA(isA<TestFailure>()));
        await operation.dispose();
        session.dispose();
      }
    },
  );
  test('missing new run ends at bounded binding clock deadline', () async {
    final session = AppSession(FakeEngine(), FakePlatform());
    final operation = ModbusOperationWait(session), clock = OperationClock();
    await expectLater(clock.wait(operation), throwsA(isA<TestFailure>()));
    expect(clock.ticks, lessThanOrEqualTo(301));
    await operation.dispose();
    session.dispose();
  });
}
