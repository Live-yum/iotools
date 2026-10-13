import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/messaging/messaging.dart';
import 'package:iotools_mobile/features/opcua/opcua_scan_result.dart';
import '../../support/fake_engine.dart';

class ScanPageEngine extends FakeEngine {
  bool expired = false, partial = false;
  String? failure;
  Completer<Object?>? pending;
  JsonMap get descriptor => {
    'run_id': 'tree',
    'node_count': 10000,
    'finished': true,
    'complete': !partial,
    'error': failure ?? (partial ? 'scan cancelled; incomplete' : ''),
  };
  @override
  Future<Object?> command(JsonMap command) async {
    if (command['op'] == 'events') {
      return {
        'events': [],
        'running': false,
        'dropped': 12000,
        'scan_result': descriptor,
      };
    }
    if (command['op'] != 'opcua.scan.page') return super.command(command);
    calls.add(cloneMap(command));
    if (pending != null) return pending!.future;
    if (expired) throw StateError('scan result expired');
    final offset = command['offset'] as int;
    return {
      'run_id': 'tree',
      'offset': offset,
      'next_offset': offset + 100,
      'scan_result': descriptor,
      'nodes': [
        for (var i = offset; i < offset + 100; i++)
          {
            'node_id': 'ns=2;s=Node$i',
            'parent_id': 'ns=2;s=Node${i ~/ 2}',
            'display_name': 'Node$i',
            'node_class': 'Variable',
            'browse_name': 'Node$i',
            'depth': 2,
          },
      ],
    };
  }
}

void main() {
  late ScanPageEngine engine;
  late AppSession session;
  setUp(() {
    engine = ScanPageEngine();
    session = AppSession(engine, FakePlatform())..ready = true;
    session.started({'run_id': 'tree'});
  });
  tearDown(() {
    if (!session.disposed) session.dispose();
  });

  testWidgets(
    'all scan nodes stay discoverable even when every event was dropped',
    (tester) async {
      await session.poll();
      expect(session.events, isEmpty);
      expect(session.scanResult['node_count'], 10000);
      expect(session.status, '已完成');
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ResultPage(
              session: session,
              onPrepare: (_) {},
              onRerun: () {},
            ),
          ),
        ),
      );
      expect(find.textContaining('扫描完整 · 已保留 10000'), findsOneWidget);
      expect(find.text('导出执行日志'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('open_scan_result')));
      await tester.pumpAndSettle();
      expect(find.text('完整扫描 · 已保留 10000 个节点'), findsOneWidget);
      expect(
        find.textContaining('ns=2;s=Node0\n父节点: ns=2;s=Node0'),
        findsOneWidget,
      );
      await tester.tap(find.byKey(const ValueKey('scan_next')));
      await tester.pumpAndSettle();
      expect(
        find.textContaining('ns=2;s=Node100\n父节点: ns=2;s=Node50'),
        findsOneWidget,
      );
      expect(find.textContaining('ns=2;s=Node0\n'), findsNothing);
      await tester.tap(find.byKey(const ValueKey('scan_previous')));
      await tester.pumpAndSettle();
      expect(
        find.text('ns=2;s=Node0\n父节点: ns=2;s=Node0\n浏览名: Node0 · 深度: 2'),
        findsOneWidget,
      );
      expect(
        engine.calls
            .where((c) => c['op'] == 'opcua.scan.page')
            .map((c) => c['offset']),
        [0, 100, 0],
      );
      expect(
        engine.calls.every(
          (c) => c['op'] == 'opcua.scan.page' && c['limit'] == 100,
        ),
        isTrue,
      );
      expect(
        session.events,
        isEmpty,
        reason: 'pages never accumulate in event history',
      );
    },
  );

  testWidgets(
    'partial and expired scans are explicit and never show another tree',
    (tester) async {
      engine.partial = true;
      await session.poll();
      expect(session.status, '扫描未完成');
      await tester.pumpWidget(
        MaterialApp(
          home: OpcuaScanResultPage(session: session, runId: 'tree'),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('部分结果，扫描未完成'), findsOneWidget);
      expect(find.text('scan cancelled; incomplete'), findsOneWidget);
      engine.expired = true;
      await tester.tap(find.byKey(const ValueKey('scan_next')));
      await tester.pumpAndSettle();
      expect(find.textContaining('scan result expired'), findsOneWidget);
      expect(find.byType(ListTile), findsNothing);
      session.started({'run_id': 'replacement'});
      expect(session.scanResult, isEmpty);
    },
  );

  testWidgets('closing during a pending page does not deliver a late tree', (
    tester,
  ) async {
    engine.pending = Completer<Object?>();
    await tester.pumpWidget(
      MaterialApp(
        home: OpcuaScanResultPage(session: session, runId: 'tree'),
      ),
    );
    await tester.pump();
    await tester.pumpWidget(const SizedBox());
    engine.pending!.complete({'run_id': 'replacement'});
    await tester.pump();
    expect(tester.takeException(), isNull);
  });

  for (final change in [
    'new run',
    'collection',
    'lifecycle epoch',
    'disposed',
  ]) {
    testWidgets('mounted page rejects late tree after $change', (tester) async {
      engine.pending = Completer<Object?>();
      await tester.pumpWidget(
        MaterialApp(
          home: OpcuaScanResultPage(session: session, runId: 'tree'),
        ),
      );
      await tester.pump();
      switch (change) {
        case 'new run':
          session.started({'run_id': 'replacement'});
        case 'collection':
          await session.switchCollection('replacement.yaml', {});
        case 'lifecycle epoch':
          await session.pause();
        case 'disposed':
          session.dispose();
      }
      engine.pending!.complete({
        'run_id': 'tree',
        'offset': 0,
        'next_offset': 1,
        'scan_result': engine.descriptor,
        'nodes': [
          {'node_id': 'old-tree', 'display_name': 'stale node'},
        ],
      });
      await tester.pumpAndSettle();
      expect(find.textContaining('扫描会话已变化'), findsOneWidget);
      expect(find.byType(ListTile), findsNothing);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('short screen keeps paging usable with a long scan error', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(320, 320));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    engine.partial = true;
    engine.failure = '服务器错误。' * 340;
    await tester.pumpWidget(
      MaterialApp(
        home: OpcuaScanResultPage(session: session, runId: 'tree'),
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(
      tester.widget<Text>(find.byKey(const ValueKey('scan_failure'))).data,
      engine.failure,
      reason: 'full failure remains scrollable, never truncated',
    );
    expect(
      find.byKey(const ValueKey('scan_next')).hitTestable(),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const ValueKey('scan_next')));
    await tester.pumpAndSettle();
    expect(engine.calls.last['offset'], 100);
    expect(tester.takeException(), isNull);
    engine.expired = true;
    await tester.tap(find.byTooltip('刷新当前页'));
    await tester.pumpAndSettle();
    expect(find.textContaining('scan result expired'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  test(
    'descriptor arriving before run reply survives event queue reconciliation',
    () async {
      session.resultRunId = '';
      await session.poll();
      session.started({'run_id': 'tree'});
      expect(session.scanResult['node_count'], 10000);
      expect(session.status, '已完成');
      expect(session.events, isEmpty);
    },
  );
}
