import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/session.dart';
import '../support/fake_engine.dart';

class LifecycleEngine extends FakeEngine {
  int pauses = 0, resumes = 0;
  Completer<void>? heldPause, heldResume;
  bool failResume = false;
  @override
  Future<void> pause() async {
    pauses++;
    await heldPause?.future;
    state['running'] = false;
  }

  @override
  Future<void> resume() async {
    resumes++;
    await heldResume?.future;
    if (failResume) throw StateError('resume failed');
  }
}

void main() {
  test(
    'duplicate lifecycle notifications share one operation and preserve memory drafts',
    () async {
      final engine = LifecycleEngine();
      final s = AppSession(engine, FakePlatform());
      await s.initialize();
      s.source = 'only memory';
      s.prepare({'id': 'draft', 'name': 'secret memory'});
      s.started({'run_id': 'r'});
      await s.resume();
      expect(engine.resumes, 0);
      engine.heldPause = Completer<void>();
      final first = s.pause(), second = s.pause();
      expect(engine.pauses, 1);
      expect(s.paused, true);
      expect(s.status, '已取消');
      final epoch = s.epoch;
      await Future<void>.delayed(Duration.zero);
      expect(s.epoch, epoch);
      engine.heldPause!.complete();
      await Future.wait([first, second]);
      engine.heldResume = Completer<void>();
      final resume = s.resume(), duplicate = s.resume();
      expect(engine.resumes, 1);
      engine.heldResume!.complete();
      await Future.wait([resume, duplicate]);
      expect(s.paused, false);
      expect(engine.calls.where((c) => c['op'] == 'state'), hasLength(1));
      expect(s.source, 'only memory');
      expect(s.draft['name'], 'secret memory');
      expect(engine.calls.where((c) => c['op'] == 'run'), isEmpty);
      s.dispose();
    },
  );
  test(
    'hidden during pending resume wins over its stale acknowledgement',
    () async {
      final engine = LifecycleEngine();
      final session = AppSession(engine, FakePlatform());
      await session.initialize();
      await session.pause();
      engine.heldResume = Completer<void>();
      final resumed = session.resume();
      await session.pause();
      engine.heldResume!.complete();
      await resumed;
      expect(session.paused, true);
      expect(engine.pauses, 2);
      expect(engine.resumes, 1);
      expect(engine.calls.where((c) => c['op'] == 'state'), isEmpty);
      session.dispose();
    },
  );
  test(
    'recovery page lifecycle never sends commands to a nonexistent session',
    () async {
      final engine = LifecycleEngine();
      final s = AppSession(engine, FakePlatform());
      await s.pause();
      await s.pause();
      await s.resume();
      await s.resume();
      expect(engine.pauses, 0);
      expect(engine.resumes, 0);
      expect(engine.calls, isEmpty);
      expect(s.paused, false);
      s.dispose();
    },
  );
  test(
    'resume failure stays paused and a later explicit transition can recover',
    () async {
      final engine = LifecycleEngine();
      final s = AppSession(engine, FakePlatform());
      await s.initialize();
      await s.pause();
      engine.failResume = true;
      await s.resume();
      expect(s.paused, true);
      expect(s.error, contains('resume failed'));
      engine.failResume = false;
      await s.resume();
      expect(s.paused, false);
      expect(engine.resumes, 2);
      s.dispose();
    },
  );
}
