import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/history/history_page.dart';
import 'package:iotools_mobile/features/messaging/messaging.dart';
import 'support/fake_engine.dart';

class HistoryEngine extends FakeEngine {
  List<JsonMap> rows = [];
  List<JsonMap> events = [];
  Completer<Object?>? pendingList;
  int lists = 0;
  bool failNextList = false;
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'history.status') return {'exists': rows.isNotEmpty};
    if (c['op'] == 'history.list') {
      lists++;
      if (failNextList) {
        failNextList = false;
        throw StateError('fixture database read failure');
      }
      final pending = pendingList;
      pendingList = null;
      return pending == null ? rows.map(cloneMap).toList() : pending.future;
    }
    if (c['op'] == 'events') {
      final batch = events;
      events = [];
      return {'events': batch, 'running': state['running']};
    }
    return super.command(c);
  }
}

class HistorySession extends AppSession {
  HistorySession(HistoryEngine engine) : super(engine, FakePlatform()) {
    state = cloneMap(engine.state);
    state['path'] = 'first.yaml';
    ready = true;
    resultRunId = 'request';
  }
  void selectCollection(String path) {
    state['path'] = path;
    notifyListeners();
  }
}

JsonMap row(String name) => {
  'id': 1, 'method': 'GET', 'recipe': name, 'status': 200,
  'created_at': '2026-10-02T00:00:00Z', 'body_bytes': 2,
};
JsonMap event(String kind) => {
  'kind': kind, 'run_id': 'request', 'data': {'status': 'completed'},
};

void main() {
  late HistoryEngine engine;
  late HistorySession session;
  setUp(() {
    engine = HistoryEngine();
    session = HistorySession(engine);
  });
  tearDown(() => session.dispose());
  Future<void> show(WidgetTester tester) async {
    await tester.pumpWidget(MaterialApp(home: Scaffold(
      body: HistoryPage(session: session, onPrepare: (_) {}),
    )));
    await tester.pumpAndSettle();
  }
  Future<void> terminal(WidgetTester tester, {String status = 'completed'}) async {
    engine.events = [{...event('done'), 'data': {'status': status}}];
    engine.state['running'] = false;
    await session.poll();
    await tester.pumpAndSettle();
  }

  for (final outcome in ['completed', 'failed', 'cancelled']) {
    testWidgets('visible history refreshes after $outcome without stream queries', (tester) async {
      engine.state['running'] = true;
      await show(tester);
      expect(find.text('暂无执行历史'), findsOneWidget);
      final before = engine.lists;
      for (var i = 0; i < 10; i++) {
        engine.events = [event('response')];
        await session.poll();
      }
      await tester.pump();
      expect(engine.lists, before);
      engine.rows = [row('saved-response')];
      await terminal(tester, status: outcome);
      expect(find.text('GET saved-response'), findsOneWidget);
      expect(engine.lists, before + 1);
    });
  }

  testWidgets('completion during pending query discards stale snapshot and coalesces reload', (tester) async {
    final pending = Completer<Object?>();
    engine.pendingList = pending;
    await tester.pumpWidget(MaterialApp(home: Scaffold(
      body: HistoryPage(session: session, onPrepare: (_) {}),
    )));
    await tester.pump();
    engine.rows = [row('latest')];
    engine.events = [event('done'), event('done')];
    await session.poll();
    pending.complete([]);
    await tester.pumpAndSettle();
    expect(find.text('GET latest'), findsOneWidget);
    expect(engine.lists, 2);
  });

  testWidgets('collection change rejects pending old rows', (tester) async {
    final pending = Completer<Object?>();
    engine.pendingList = pending;
    await tester.pumpWidget(MaterialApp(home: Scaffold(
      body: HistoryPage(session: session, onPrepare: (_) {}),
    )));
    await tester.pump();
    engine.rows = [row('new-collection')];
    session.selectCollection('second.yaml');
    pending.complete([row('old-collection')]);
    await tester.pumpAndSettle();
    expect(find.text('GET old-collection'), findsNothing);
    expect(find.text('GET new-collection'), findsOneWidget);
  });

  testWidgets('leaving and reentering loads rows completed off-page', (tester) async {
    await show(tester);
    await tester.pumpWidget(const MaterialApp(home: Text('requests')));
    await tester.pumpAndSettle();
    final before = engine.lists;
    engine.rows = [row('finished-away')];
    await terminal(tester);
    expect(engine.lists, before);
    await show(tester);
    expect(find.text('GET finished-away'), findsOneWidget);
    expect(engine.lists, before + 1);
  });

  testWidgets('read failure remains visible and manual retry recovers', (tester) async {
    engine.failNextList = true;
    await show(tester);
    expect(find.textContaining('fixture database read failure'), findsOneWidget);
    engine.rows = [row('after-retry')];
    await tester.tap(find.text('刷新'));
    await tester.pumpAndSettle();
    expect(find.text('GET after-retry'), findsOneWidget);
    expect(find.textContaining('fixture database read failure'), findsNothing);
  });

  testWidgets('disposing with an outstanding load does not update detached UI', (tester) async {
    final pending = Completer<Object?>();
    engine.pendingList = pending;
    await tester.pumpWidget(MaterialApp(home: Scaffold(
      body: HistoryPage(session: session, onPrepare: (_) {}),
    )));
    await tester.pump();
    await tester.pumpWidget(const SizedBox());
    pending.complete([row('late')]);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('read-only recording notice does not turn completed HTTP into failure', (tester) async {
    engine.events = [
      {'kind': 'history_status', 'run_id': 'request', 'data': {
        'recorded': false, 'reason': 'read_only', 'message': '只读保护，本次未记录',
      }},
      event('done'),
    ];
    await session.poll();
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: ResultPage(
      session: session, onPrepare: (_) {}, onRerun: () {},
    ))));
    await tester.pumpAndSettle();
    expect(find.text('只读保护，本次未记录'), findsOneWidget);
    expect(find.text('已完成'), findsOneWidget);
    expect(session.error, isNull);
  });

  testWidgets('disabled history stays disabled and empty after completion', (tester) async {
    await show(tester);
    await terminal(tester);
    expect(session.history, isFalse);
    expect(find.text('暂无执行历史'), findsOneWidget);
    expect(engine.calls.where((c) => c['op'] == 'options.set'), isEmpty);
    expect(find.textContaining('HTTP 历史当前关闭'), findsOneWidget);
  });
}
