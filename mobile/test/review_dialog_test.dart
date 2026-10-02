import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/shared/review_dialog.dart';
import 'package:iotools_mobile/core/json.dart';

void main() {
  testWidgets(
    'review distinguishes payload changes and masks credentials without mutating source',
    (t) async {
      final r = <String, dynamic>{
        'protocol': 'http',
        'action': 'POST',
        'endpoint': 'https://user:SECRET@example.com/a?token=TOKEN',
        'params': {
          'body': 'first payload',
          'headers': {
            'Authorization': 'Bearer DO_NOT_SHOW',
            'Content-Type': 'text/plain',
          },
          'crypto': {
            'x': {'key': 'KEY'},
          },
        },
      };
      bool? accepted;
      await t.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () async {
                  accepted = await reviewExecution(c, {'request': r});
                },
                child: const Text('预览'),
              ),
            ),
          ),
        ),
      );
      await t.tap(find.text('预览'));
      await t.pumpAndSettle();
      expect(find.text('first payload'), findsOneWidget);
      await t.tap(find.text('请求头（凭据已隐藏）'));
      await t.pumpAndSettle();
      expect(find.textContaining('DO_NOT_SHOW'), findsNothing);
      expect(find.textContaining('SECRET'), findsNothing);
      expect(find.textContaining('TOKEN'), findsNothing);
      expect(
        mapOf(mapOf(r['params'])['headers'])['Authorization'],
        'Bearer DO_NOT_SHOW',
      );
      await t.tap(find.text('取消'));
      await t.pumpAndSettle();
      expect(accepted, false);
      mapOf(r['params'])['body'] = 'second payload';
      r['params'] = {...mapOf(r['params']), 'body': 'second payload'};
      await t.tap(find.text('预览'));
      await t.pumpAndSettle();
      expect(find.text('second payload'), findsOneWidget);
      expect(find.text('first payload'), findsNothing);
      await t.tap(find.byKey(const ValueKey('confirm_action')));
      await t.pumpAndSettle();
      expect(accepted, true);
    },
  );
}
