import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/features/requests/request_form.dart';
import 'package:iotools_mobile/features/messaging/retained_preview.dart';

void main() {
  testWidgets('invalid JSON keeps editor open, correction retains UInt64', (
    t,
  ) async {
    Object? value;
    await t.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (c) => Scaffold(
            body: TextButton(
              onPressed: () async {
                value = await editJsonValue(c, 'JSON', {});
              },
              child: const Text('编辑'),
            ),
          ),
        ),
      ),
    );
    await t.tap(find.text('编辑'));
    await t.pumpAndSettle();
    await t.enterText(find.byKey(const ValueKey('json_value_editor')), '{"x":');
    await t.tap(find.text('应用'));
    await t.pumpAndSettle();
    expect(find.byKey(const ValueKey('json_value_editor')), findsOneWidget);
    expect(find.textContaining('JSON 格式错误'), findsOneWidget);
    await t.enterText(
      find.byKey(const ValueKey('json_value_editor')),
      '{"x":18446744073709551615}',
    );
    await t.tap(find.text('应用'));
    await t.pumpAndSettle();
    expect(exactEncode(value), '{"x":18446744073709551615}');
  });
  testWidgets(
    'retained search changes only presentation, preparation preserves full exact snapshot',
    (t) async {
      JsonMap? draft;
      final topics = List.generate(40, (i) => '/a//$i/');
      await t.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: RetainedPreview(
                snapshot: {
                  'topics': topics,
                  'confirm_token': 'nonce',
                  'scan_duration_ms': 1000,
                },
                request: {
                  'id': 'x',
                  'protocol': 'mqtt',
                  'action': 'preview-retained',
                  'params': {'topic': '/a/#'},
                },
                onPrepare: (v) => draft = v,
              ),
            ),
          ),
        ),
      );
      await t.enterText(find.byType(TextField), '39');
      await t.pumpAndSettle();
      expect(find.text('/a//39/'), findsOneWidget);
      final action = find.text('准备清理完整快照（40 个精确主题）');
      await t.ensureVisible(action);
      await t.tap(action);
      expect(mapOf(draft!['params'])['confirm_topics'], topics);
      expect(mapOf(draft!['params'])['confirm_token'], 'nonce');
      expect(draft!['action'], 'clean-retained');
    },
  );
}
