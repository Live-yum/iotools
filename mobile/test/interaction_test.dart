import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'support/fake_engine.dart';

void main() {
  testWidgets(
    'typed options use original selection index and cancellation stays explicit',
    (t) async {
      final engine = FakeEngine()..consumePendingOnce = true;
      await t.pumpWidget(IotoolsApp(engine: engine, platform: FakePlatform()));
      await t.pumpAndSettle();
      for (final index in [0, 1, 2]) {
        engine.pendingEvents = Completer()
          ..complete({
            'events': [
              {
                'run_id': 'interaction-run',
                'seq': index,
                'kind': 'interaction',
                'data': {
                  'interaction_id': 'choice-$index',
                  'type': 'select',
                  'title': '选择原始值',
                  'options': [
                    7,
                    true,
                    {'name': '中文'},
                  ],
                },
              },
            ],
            'running': false,
          });
        await t.pump(const Duration(milliseconds: 300));
        await t.pump(const Duration(milliseconds: 300));
        final choices = find.descendant(
          of: find.byType(AlertDialog),
          matching: find.byType(ListTile),
        );
        await t.tap(choices.at(index));
        await t.pump();
        await t.tap(find.text('确认'));
        await t.pumpAndSettle();
        final response = engine.calls.lastWhere((c) => c['op'] == 'respond');
        expect(response['selection_index'], index);
        expect(response.containsKey('value'), isFalse);
        expect(response['confirmed'], true);
      }
      engine.pendingEvents = Completer()
        ..complete({
          'events': [
            {
              'run_id': 'interaction-run',
              'seq': 4,
              'kind': 'interaction',
              'data': {
                'interaction_id': 'cancel',
                'type': 'prompt',
                'title': '输入测试值',
                'sensitive': true,
              },
            },
          ],
          'running': false,
        });
      await t.pump(const Duration(milliseconds: 300));
      await t.pump(const Duration(milliseconds: 300));
      expect(
        t
            .widget<TextField>(
              find.descendant(
                of: find.byType(AlertDialog),
                matching: find.byType(TextField),
              ),
            )
            .obscureText,
        true,
      );
      await t.tap(find.text('取消'));
      await t.pumpAndSettle();
      expect(
        engine.calls.lastWhere((c) => c['op'] == 'respond')['confirmed'],
        false,
      );
      expect(engine.calls.where((c) => c['op'] == 'run'), isEmpty);
      await t.pumpWidget(const SizedBox());
      await t.pump();
    },
  );
}
