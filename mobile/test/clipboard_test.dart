import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/shared/widgets.dart';

void main() {
  test('clipboard UTF-8 boundary counts Chinese and emoji bytes', () {
    expect(fitsClipboard('a' * maxClipboardBytes), true);
    expect(fitsClipboard('a' * (maxClipboardBytes + 1)), false);
    expect(fitsClipboard('中' * (maxClipboardBytes ~/ 3)), true);
    expect(fitsClipboard('中' * (maxClipboardBytes ~/ 3 + 1)), false);
    expect(fitsClipboard('😀' * (maxClipboardBytes ~/ 4)), true);
    expect(fitsClipboard('😀' * (maxClipboardBytes ~/ 4 + 1)), false);
  });
  testWidgets(
    'large results refuse clipboard without truncation and platform errors are surfaced',
    (t) async {
      final calls = <MethodCall>[];
      bool fail = false;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
            if (call.method == 'Clipboard.setData') {
              calls.add(call);
              if (fail)
                throw PlatformException(
                  code: 'binder',
                  message: 'test failure',
                );
            }
            return null;
          });
      addTearDown(
        () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(SystemChannels.platform, null),
      );
      String content = '中' * 100000;
      bool? result;
      await t.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () async {
                  result = await copyText(c, content);
                },
                child: const Text('复制'),
              ),
            ),
          ),
        ),
      );
      await t.tap(find.text('复制'));
      await t.pump();
      expect(result, false);
      expect(calls, isEmpty);
      expect(find.textContaining('请使用导出保存完整内容'), findsOneWidget);
      content = '中文😀';
      fail = true;
      await t.tap(find.text('复制'));
      await t.pump();
      expect(result, false);
      expect(calls.single.arguments, {'text': '中文😀'});
      expect(t.takeException(), isNull);
    },
  );
}
