import 'dart:io';
import 'dart:ui' as ui;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/json.dart';
import 'support/fake_engine.dart';

class FeedbackEngine extends FakeEngine {
  FeedbackEngine() {
    state['requests'] = [
      {
        'id': 'get',
        'name': '本地 HTTP',
        'protocol': 'http',
        'action': 'GET',
        'endpoint': r'${http}/health',
        'params': {},
      },
    ];
  }
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'config.get')
      return {
        'source': 'version: 1',
        'collection': {
          'profiles': {
            '本地': {'http': 'http://127.0.0.1:8080'},
          },
        },
      };
    if (c['op'] == 'history.status') {
      calls.add(c);
      return {'exists': false, 'enabled': false};
    }
    return super.command(c);
  }
}

Future<void> screenshot(WidgetTester tester, GlobalKey key, String name) async {
  await tester.runAsync(() async {
    final boundary =
        key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final image = await boundary.toImage(pixelRatio: 1);
    final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
    final file = File('build/flutter-evidence/$name.png');
    await file.parent.create(recursive: true);
    await file.writeAsBytes(bytes!.buffer.asUint8List());
    image.dispose();
  });
}

void main() {
  setUpAll(() async {
    final icons = File(
      'build/unit_test_assets/fonts/MaterialIcons-Regular.otf',
    );
    if (icons.existsSync()) {
      final loader = FontLoader('MaterialIcons');
      loader.addFont(
        Future.value(ByteData.sublistView(await icons.readAsBytes())),
      );
      await loader.load();
    }
    final file = File('/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc');
    if (file.existsSync()) {
      final loader = FontLoader('Roboto');
      loader.addFont(
        Future.value(ByteData.sublistView(await file.readAsBytes())),
      );
      await loader.load();
    }
  });
  for (final scale in [1.0, 2.0]) {
    testWidgets(
      'phone feedback readable endpoint and missing history at scale $scale',
      (tester) async {
        tester.view.physicalSize = const Size(320, 752);
        tester.view.devicePixelRatio = 1;
        tester.platformDispatcher.textScaleFactorTestValue = scale;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
        final e = FeedbackEngine();
        final capture = GlobalKey();
        await tester.pumpWidget(
          RepaintBoundary(
            key: capture,
            child: IotoolsApp(engine: e, platform: FakePlatform()),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('GET  http://127.0.0.1:8080/health'), findsOneWidget);
        expect(find.textContaining(r'$%7B'), findsNothing);
        expect(tester.takeException(), isNull);
        await screenshot(tester, capture, 'feedback-home-$scale');
        await tester.tap(find.byKey(const ValueKey('nav_history')));
        await tester.pumpAndSettle();
        for (final label in ['SQL 查询 / 事务', '集合管理']) {
          final button = find.widgetWithText(OutlinedButton, label);
          expect(tester.widget<OutlinedButton>(button).onPressed, isNull);
        }
        expect(find.textContaining('暂无历史数据库'), findsOneWidget);
        expect(
          e.calls.where(
            (c) =>
                c['op'] == 'history.query' || c['op'] == 'history.collections',
          ),
          isEmpty,
        );
        expect(tester.takeException(), isNull);
        await screenshot(tester, capture, 'feedback-history-$scale');
        await tester.pumpWidget(const SizedBox());
        await tester.pump();
      },
    );
  }
}
