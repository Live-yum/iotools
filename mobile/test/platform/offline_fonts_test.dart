import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/shared/widgets.dart';

void main() {
  test('desktop theme and every code text style retain CJK and emoji fallback', () {
    try {
      for (final platform in [TargetPlatform.linux, TargetPlatform.windows, TargetPlatform.macOS]) {
        debugDefaultTargetPlatformOverride = platform;
        for (final brightness in Brightness.values) {
          expect(appTheme(brightness).textTheme.bodyMedium!.fontFamilyFallback,
              offlineFontFallback);
        }
        for (final size in [13.0, 14.0]) {
          expect(codeTextStyle(fontSize: size).fontFamily, 'monospace');
          expect(codeTextStyle(fontSize: size).fontFamilyFallback,
              offlineFontFallback);
        }
      }
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('short and paged result fields keep offline fallback after merge', (tester) async {
    for (final viewer in [const DataView('响应中文😀'), const PagedText('配置中文😀')]) {
      await tester.pumpWidget(MaterialApp(
        theme: appTheme(Brightness.dark),
        home: Scaffold(body: SingleChildScrollView(child: viewer)),
      ));
      await tester.pump();
      final fields = tester.widgetList<EditableText>(find.byType(EditableText));
      expect(fields, isNotEmpty);
      for (final field in fields) {
        expect(field.style.fontFamilyFallback, offlineFontFallback);
      }
      for (final text in tester.widgetList<SelectableText>(find.byType(SelectableText))) {
        expect(text.semanticsLabel, text.data);
      }
      expect(tester.takeException(), isNull);
    }
  }, variant: TargetPlatformVariant({TargetPlatform.linux, TargetPlatform.windows, TargetPlatform.macOS}));
}
