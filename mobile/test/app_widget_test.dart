import 'dart:io';
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'support/fake_engine.dart';

void main() {
  setUpAll(() async {
    final fontFile = File(
      'build/unit_test_assets/fonts/MaterialIcons-Regular.otf',
    );
    if (fontFile.existsSync()) {
      final icons = FontLoader('MaterialIcons');
      icons.addFont(
        Future.value(ByteData.sublistView(await fontFile.readAsBytes())),
      );
      await icons.load();
    }
    final path = '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc';
    if (File(path).existsSync()) {
      final loader = FontLoader('Roboto');
      loader.addFont(
        Future.value(ByteData.sublistView(await File(path).readAsBytes())),
      );
      await loader.load();
    }
  });
  testWidgets(
    'three navigation destinations, native draft controls and no implicit network',
    (tester) async {
      final e = FakeEngine();
      await tester.pumpWidget(IotoolsApp(engine: e, platform: FakePlatform()));
      await tester.pumpAndSettle();
      expect(find.byType(NavigationDestination), findsNWidgets(3));
      await tester.tap(find.text('设备状态查询'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('request_endpoint')),
        'http://127.0.0.1:12345/中文😀',
      );
      await tester.tap(find.byKey(const ValueKey('nav_settings')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('nav_requests')));
      await tester.pumpAndSettle();
      expect(find.text('http://127.0.0.1:12345/中文😀'), findsOneWidget);
      expect(e.calls.where((c) => c['op'] == 'run'), isEmpty);
      await tester.tap(find.byKey(const ValueKey('run_request')));
      await tester.pump(const Duration(milliseconds: 500));
      expect(e.calls.where((c) => c['op'] == 'run').length, 1);
      expect(find.text('执行中'), findsWidgets);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );
  testWidgets(
    'Flutter request cards render phone and landscape screenshots without overflow',
    (tester) async {
      final key = GlobalKey();
      await tester.binding.setSurfaceSize(const Size(480, 800));
      await tester.pumpWidget(
        RepaintBoundary(
          key: key,
          child: IotoolsApp(engine: FakeEngine(), platform: FakePlatform()),
        ),
      );
      await tester.pumpAndSettle();
      await capture(tester, key, 'flutter-phone');
      expect(tester.takeException(), isNull);
      await tester.binding.setSurfaceSize(const Size(960, 540));
      await tester.pumpAndSettle();
      await capture(tester, key, 'flutter-landscape');
      expect(tester.takeException(), isNull);
      await tester.binding.setSurfaceSize(const Size(320, 640));
      await tester.pumpAndSettle();
      await capture(tester, key, 'flutter-narrow');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      await tester.binding.setSurfaceSize(null);
    },
  );
}

Future<void> capture(WidgetTester tester, GlobalKey key, String name) async {
  await tester.runAsync(() async {
    final boundary =
        key.currentContext!.findRenderObject() as RenderRepaintBoundary;
    final image = await boundary.toImage(pixelRatio: 1);
    final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
    final dir = Directory('build/flutter-evidence')
      ..createSync(recursive: true);
    await File(
      '${dir.path}/$name.png',
    ).writeAsBytes(bytes!.buffer.asUint8List());
    image.dispose();
  });
}
