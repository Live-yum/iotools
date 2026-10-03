import 'dart:convert';
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
JsonMap probe(
  String run,
  int unit, {
  bool responsive = true,
  bool exception = false,
  String error = '',
}) => {
  'run_id': run,
  'kind': 'unit-probe',
  'data': {
    'unit': unit,
    'responsive': responsive,
    'exception': exception,
    if (error.isNotEmpty) 'error': error,
  },
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
  test('delayed scan waits for retained exception and successful probe', () async {
    final engine = QueuedOperationEngine();
    final session = AppSession(engine, FakePlatform())..ready = true;
    session.resultRunId = 'old';
    session.events.addAll([started('old'), probe('old', 1), done('old')]);
    final operation = ModbusOperationWait(session), clock = OperationClock();
    clock.onTick = (tick) async {
      if (tick == 2) {
        session.started({'run_id': 'scan'});
        engine.pending.addAll([
          started('scan', 'scan-units'),
          probe('scan', 2, responsive: false, exception: true,
              error: 'modbus: exception 11'),
        ]);
        await session.poll();
        // Native idle alone is deliberately insufficient: no terminal or unit 1.
        expect(session.running, isFalse);
      }
      if (tick == 7) {
        engine.pending.addAll([probe('scan', 1), done('scan')]);
        await session.poll();
      }
    };
    final run = await clock.wait(operation, 'scan-units');
    expect(clock.ticks, 7);
    expectModbusUnitProbe(session, run,
        unit: 2, responsive: false, exception: true);
    expectModbusUnitProbe(session, run, unit: 1, responsive: true);
    await operation.dispose();
    session.dispose();
  });
  test('prior-run success cannot satisfy retained same-run probe', () async {
    final session = AppSession(FakeEngine(), FakePlatform());
    final operation = ModbusOperationWait(session), clock = OperationClock();
    session.started({'run_id': 'scan'});
    session.events.addAll([
      started('scan', 'scan-units'),
      probe('old', 1),
      probe('scan', 2, responsive: false, exception: true),
      done('scan'),
    ]);
    final run = await clock.wait(operation, 'scan-units');
    expect(() => expectModbusUnitProbe(session, run, unit: 1, responsive: true),
        throwsA(isA<TestFailure>()));
    expect(() => expectModbusUnitProbe(session, 'old', unit: 1, responsive: true),
        throwsA(isA<TestFailure>()));
    expect(modbusRunEvidence(session), isNot(contains('"run_id":"old"')));
    await operation.dispose();
    session.dispose();
  });
  test('fast scan probes must remain in UI state, not only the stream', () async {
    final engine = QueuedOperationEngine();
    final session = AppSession(engine, FakePlatform())..ready = true;
    final operation = ModbusOperationWait(session), clock = OperationClock();
    engine.pending.addAll([
      started('scan', 'scan-units'),
      probe('scan', 2, responsive: false, exception: true),
      probe('scan', 1),
      done('scan'),
    ]);
    await session.poll();
    session.started({'run_id': 'scan'});
    final run = await clock.wait(operation, 'scan-units');
    expectModbusUnitProbe(session, run,
        unit: 2, responsive: false, exception: true);
    expectModbusUnitProbe(session, run, unit: 1, responsive: true);
    session.events.removeWhere((event) => event['kind'] == 'unit-probe');
    expect(operation.observed.where((e) => e['kind'] == 'unit-probe').length, 2);
    expect(() => expectModbusUnitProbe(session, run, unit: 1, responsive: true),
        throwsA(isA<TestFailure>()));
    await operation.dispose();
    session.dispose();
  });
  test('completed scan preserves genuine same-run timeout as a failure', () async {
    final session = AppSession(FakeEngine(), FakePlatform());
    final operation = ModbusOperationWait(session), clock = OperationClock();
    session.started({'run_id': 'scan'});
    session.events.addAll([
      started('scan', 'scan-units'),
      probe('scan', 2, responsive: false, exception: true),
      probe('scan', 1, responsive: false, error: 'read tcp: i/o timeout'),
      done('scan'),
    ]);
    final run = await clock.wait(operation, 'scan-units');
    expect(() => expectModbusUnitProbe(session, run, unit: 1, responsive: true),
        throwsA(isA<TestFailure>().having(
            (error) => '$error', 'diagnostics', contains('i/o timeout'))));
    expect(() => expectModbusUnitProbe(session, run,
        unit: 2, responsive: false), throwsA(isA<TestFailure>()));
    await operation.dispose();
    session.dispose();
  });
  test('expected audit failure still requires a new matching run', () async {
    final session = AppSession(FakeEngine(), FakePlatform());
    final operation = ModbusOperationWait(session), clock = OperationClock();
    session.started({'run_id': 'audit'});
    session.events.addAll([
      started('audit'), done('audit', 'failed', 'audit open failed'),
    ]);
    expect(await operation.waitForOperation(
        action: 'write-typed', expectedStatus: 'failed',
        clock: () => clock.now, advance: clock.advance), 'audit');
    await operation.dispose();
    session.dispose();
  });
  test('stream evidence bounds bytes and retains a full new 32-unit scan', () async {
    final engine = QueuedOperationEngine();
    final session = AppSession(engine, FakePlatform())..ready = true;
    session.resultRunId = 'old';
    final operation = ModbusOperationWait(session), clock = OperationClock();
    final large = List.filled(2048, '界').join();
    for (var i = 0; i < 80; i++) {
      engine.pending.add({
        'run_id': 'old', 'kind': 'unit-probe',
        'data': {
          'source': {'action': large, 'endpoint': large, 'unit': large},
          for (final key in ['unit', 'responsive', 'exception', 'type',
            'address', 'count', 'status', 'error']) key: large,
        },
      });
    }
    await session.poll();
    int bytes() => operation.observed.fold(0,
        (total, event) => total + utf8.encode(jsonEncode(event)).length);
    expect(operation.observed.length, lessThan(64));
    expect(bytes(), lessThanOrEqualTo(256 * 1024));
    session.started({'run_id': 'scan'});
    engine.pending.add(started('scan', 'scan-units'));
    for (var unit = 1; unit <= 32; unit++) {
      engine.pending.add(probe('scan', unit,
          responsive: false, exception: true, error: large));
      engine.pending.add({
        'run_id': 'scan', 'kind': 'registers', 'data': large,
      });
    }
    engine.pending.add(done('scan'));
    await session.poll();
    expect(await clock.wait(operation, 'scan-units'), 'scan');
    final retained = operation.observed.where((e) => e['run_id'] == 'scan');
    expect(retained.length, 34);
    expect(retained.first['kind'], 'started');
    expect(retained.last['kind'], 'done');
    expect(retained.where((e) => e['kind'] == 'unit-probe').length, 32);
    expect(operation.observed.length, lessThanOrEqualTo(64));
    expect(bytes(), lessThanOrEqualTo(256 * 1024));
    final evidence = operation.evidence('scan');
    expect(evidence.length, lessThanOrEqualTo(8192));
    expect(utf8.encode(evidence).length, lessThanOrEqualTo(32768));
    expect(evidence, contains('retained='));
    expect(evidence, contains('observed='));
    expect(evidence, isNot(contains('registers')));
    await operation.dispose();
    session.dispose();
  });
  test('disposing the waiter cancels its stream subscription', () async {
    final engine = QueuedOperationEngine();
    final session = AppSession(engine, FakePlatform())..ready = true;
    final operation = ModbusOperationWait(session);
    session.started({'run_id': 'scan'});
    engine.pending.add(started('scan', 'scan-units'));
    await session.poll();
    expect(operation.observed.length, 1);
    await operation.dispose();
    engine.pending.addAll([probe('scan', 1), done('scan')]);
    await session.poll();
    expect(operation.observed.length, 1);
    expectModbusUnitProbe(session, 'scan', unit: 1, responsive: true);
    session.dispose();
  });
}
