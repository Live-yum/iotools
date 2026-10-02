import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/history/history_storage.dart';
import 'support/fake_engine.dart';

class StorageEngine extends FakeEngine {
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'history.retention.preview') {
      calls.add(cloneMap(c));
      return {'token': 'preview', 'delete_entries': c['policy']['mode'] == 'prune' ? 3 : 0,
        'delete_bytes': 100, 'keep_entries': 5};
    }
    if (c['op'] == 'history.retention.apply' || c['op'] == 'history.compact') {
      calls.add(cloneMap(c));
      return {};
    }
    return super.command(c);
  }
}

void main() {
  test('physical bytes are formatted independently of content limits', () {
    expect(historyBytes(12), '12 B');
    expect(historyBytes(2048), '2.0 KiB');
    expect(historyBytes(2097152), '2.0 MiB');
  });
  testWidgets('storage card shows real physical and reusable bytes', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: Scaffold(body: HistoryStorageCard(storage: {
      'entries': 10, 'database_bytes': 2048, 'sidecar_bytes': 1024,
      'total_bytes': 3072, 'payload_bytes': 100, 'reusable_bytes': 512,
      'policy': {'mode': 'stop'},
    }))));
    expect(find.textContaining('实际磁盘 3.0 KiB'), findsOneWidget);
    expect(find.textContaining('可复用空闲页 512 B'), findsOneWidget);
    expect(find.textContaining('不自动删除'), findsOneWidget);
  });
  for (final accept in [false, true]) {
    testWidgets('retention preview requires explicit impact approval $accept', (tester) async {
      final engine = StorageEngine();
      final session = AppSession(engine, FakePlatform());
      addTearDown(session.dispose);
      await tester.pumpWidget(MaterialApp(home: Scaffold(body: Builder(builder: (context) => TextButton(
        onPressed: () => configureHistoryStorage(context, session, {}), child: const Text('open'),
      )))));
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      expect(tester.widget<CheckboxListTile>(find.byType(CheckboxListTile)).value, false);
      await tester.tap(find.text('预览影响'));
      await tester.pumpAndSettle();
      expect(engine.calls.where((c) => c['op'] == 'history.retention.apply'), isEmpty);
      expect(find.textContaining('将永久删除 0 条记录'), findsOneWidget);
      await tester.tap(find.text(accept ? '保存限制' : '取消'));
      await tester.pumpAndSettle();
      expect(engine.calls.where((c) => c['op'] == 'history.retention.apply').length, accept ? 1 : 0);
    });
  }
}
