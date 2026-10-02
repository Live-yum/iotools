import 'dart:io';
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/shared/widgets.dart';
import 'package:iotools_mobile/features/modbus/modbus_widgets.dart';

void main() {
  setUpAll(() async {
    final icons = File('build/unit_test_assets/fonts/MaterialIcons-Regular.otf');
    if (icons.existsSync()) {
      final loader = FontLoader('MaterialIcons');
      loader.addFont(Future.value(ByteData.sublistView(await icons.readAsBytes())));
      await loader.load();
    }
    final file = File('/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc');
    if (file.existsSync()) {
      final loader = FontLoader('Roboto');
      loader.addFont(Future.value(ByteData.sublistView(await file.readAsBytes())));
      await loader.load();
    }
  });

  for (final item in [
    (const Size(320, 640), 1.0),
    (const Size(320, 640), 1.6),
    (const Size(320, 640), 2.0),
    (const Size(480, 800), 1.0),
    (const Size(960, 540), 1.6),
  ]) {
    testWidgets('shared action spacing stays reachable at ${item.$1} scale ${item.$2}', (tester) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      tester.view.physicalSize = item.$1;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final capture = GlobalKey();
      var taps = 0;
      Widget button(String key, String label, {bool icon = false}) => icon
          ? OutlinedButton.icon(key: ValueKey(key), onPressed: () => taps++,
              icon: const Icon(Icons.refresh), label: Text(label))
          : OutlinedButton(key: ValueKey(key), onPressed: () => taps++, child: Text(label));
      await tester.pumpWidget(MaterialApp(
        theme: appTheme(Brightness.dark),
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(item.$2)),
          child: child!,
        ),
        home: RepaintBoundary(key: capture, child: Scaffold(
          appBar: AppBar(title: const Text('配置与历史操作')),
          body: SingleChildScrollView(
            padding: const EdgeInsets.all(16),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Section('本地配置', children: [
                button('validate', '校验配置'),
                button('export', '导出当前 YAML'),
              ]),
              const SizedBox(height: 16),
              ActionWrap(padding: const EdgeInsets.only(bottom: 12), children: [
                button('refresh', '刷新', icon: true),
                button('sql', 'SQL 查询 / 事务'),
                button('collections', '集合管理'),
              ]),
              const TextField(key: ValueKey('filter'),
                  decoration: InputDecoration(labelText: '筛选请求 / 状态 / 时间')),
            ]),
          ),
        )),
      ));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      final first = tester.getRect(find.byKey(const ValueKey('validate')));
      final second = tester.getRect(find.byKey(const ValueKey('export')));
      expect(second.top - first.bottom, closeTo(controlGap, .01));
      final actions = [
        for (final key in ['refresh', 'sql', 'collections'])
          tester.getRect(find.byKey(ValueKey(key))),
      ]..sort((a, b) => a.top.compareTo(b.top));
      final rows = <Rect>[];
      for (final rect in actions) {
        expect(rect.height, greaterThanOrEqualTo(48));
        expect(rect.left, greaterThanOrEqualTo(16));
        expect(rect.right, lessThanOrEqualTo(item.$1.width - 16));
        if (rows.isEmpty || rect.top >= rows.last.bottom) {
          rows.add(rect);
        } else {
          rows[rows.length - 1] = rows.last.expandToInclude(rect);
        }
      }
      if (item.$1.width == 320) expect(rows.length, greaterThan(1));
      for (var index = 1; index < rows.length; index++) {
        expect(rows[index].top - rows[index - 1].bottom, closeTo(controlGap, .01));
      }
      final field = tester.getRect(find.byKey(const ValueKey('filter')));
      expect(field.top - rows.last.bottom, closeTo(12, .01));
      expect(first.height, greaterThanOrEqualTo(48));
      expect(second.height, greaterThanOrEqualTo(48));
      if ((item.$1.width == 480 && item.$2 == 1) ||
          (item.$1.width == 320 && item.$2 == 1.6)) {
        await tester.runAsync(() async {
          final boundary = capture.currentContext!.findRenderObject()! as RenderRepaintBoundary;
          final image = await boundary.toImage(pixelRatio: 1);
          final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
          final output = File('build/flutter-evidence/spacing-${item.$1.width.toInt()}-${item.$2}.png');
          await output.parent.create(recursive: true);
          await output.writeAsBytes(bytes!.buffer.asUint8List());
          image.dispose();
        });
      }
      for (final key in ['validate', 'export', 'refresh', 'sql', 'collections']) {
        final target = find.byKey(ValueKey(key));
        await Scrollable.ensureVisible(tester.element(target), alignment: .5);
        await tester.pumpAndSettle();
        await tester.tap(target);
        await tester.pumpAndSettle();
      }
      expect(taps, 5);
      await tester.ensureVisible(find.byKey(const ValueKey('filter')));
      await tester.pumpAndSettle();
      await tester.enterText(find.byKey(const ValueKey('filter')), '状态');
      expect(find.text('状态'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    });
  }

  testWidgets('Section respects existing explicit spacers without double gaps', (tester) async {
    await tester.pumpWidget(MaterialApp(theme: appTheme(Brightness.dark), home: Scaffold(
      body: Section('已有间隔', children: [
        OutlinedButton(key: const ValueKey('before'), onPressed: () {}, child: const Text('上一个')),
        const SizedBox(height: 12),
        OutlinedButton(key: const ValueKey('after'), onPressed: () {}, child: const Text('下一个')),
      ]),
    )));
    final a = tester.getRect(find.byKey(const ValueKey('before')));
    final b = tester.getRect(find.byKey(const ValueKey('after')));
    expect(b.top - a.bottom, closeTo(12, .01));
  });

  testWidgets('Modbus cards use the same adjacent-control gap', (tester) async {
    await tester.pumpWidget(MaterialApp(theme: appTheme(Brightness.dark), home: Scaffold(
      body: mbCard('本机工具', [
        OutlinedButton(key: const ValueKey('before'), onPressed: () {}, child: const Text('检查配置')),
        OutlinedButton(key: const ValueKey('after'), onPressed: () {}, child: const Text('导出配置')),
      ]),
    )));
    final a = tester.getRect(find.byKey(const ValueKey('before')));
    final b = tester.getRect(find.byKey(const ValueKey('after')));
    expect(b.top - a.bottom, closeTo(controlGap, .01));
  });
  testWidgets('existing button padding contributes to the gap instead of doubling it', (tester) async {
    await tester.pumpWidget(MaterialApp(theme: appTheme(Brightness.dark), home: Scaffold(
      body: mbCard('已有按钮留白', [
        Padding(padding: const EdgeInsets.symmetric(vertical: 4),
            child: OutlinedButton(key: const ValueKey('before'), onPressed: () {}, child: const Text('上一个'))),
        Padding(padding: const EdgeInsets.symmetric(vertical: 4),
            child: OutlinedButton(key: const ValueKey('after'), onPressed: () {}, child: const Text('下一个'))),
      ]),
    )));
    final a = tester.getRect(find.byKey(const ValueKey('before')));
    final b = tester.getRect(find.byKey(const ValueKey('after')));
    expect(b.top - a.bottom, closeTo(controlGap, .01));
  });
}
