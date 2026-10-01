import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/http/crypto_editor.dart';

void main() {
  testWidgets(
    'response body editor always emits required JSON parse contract',
    (tester) async {
      Map<String, dynamic>? result;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Builder(
              builder: (context) => TextButton(
                onPressed: () async {
                  result = await editTransform(
                    context,
                    {'type': 'decrypt'},
                    response: true,
                    codecs: ['aes'],
                  );
                },
                child: const Text('edit'),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('edit'));
      await tester.pumpAndSettle();
      expect(find.text('正文转换须为第一步；转换后解析 JSON，再继续字段转换'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('transform_apply')));
      await tester.pumpAndSettle();
      expect(result, {
        'type': 'decrypt',
        'crypto': 'aes',
        'scope': 'body',
        'text_encoding': 'utf8',
        'parse': 'json',
      });
    },
  );
}
