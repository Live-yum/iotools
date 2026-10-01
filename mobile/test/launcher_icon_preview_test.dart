import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('original avatar remains inside adaptive and legacy masks', (
    tester,
  ) async {
    const path = 'android/app/src/main/res/';
    final bytes = File(
      '${path}drawable-nodpi/iotools_avatar.png',
    ).readAsBytesSync();
    double inset(String file) =>
        double.parse(
          RegExp(
            r'android:inset="([0-9.]+)%"',
          ).firstMatch(File('$path$file').readAsStringSync())!.group(1)!,
        ) /
        100;
    final adaptive =
        1.5 * (1 - 2 * inset('drawable/ic_launcher_foreground.xml'));
    final legacy = 1 - 2 * inset('drawable/ic_launcher_legacy_foreground.xml');
    for (final resource in [
      'ic_launcher_foreground',
      'ic_launcher_legacy_foreground',
    ]) {
      expect(
        File('${path}drawable/$resource.xml').readAsStringSync(),
        contains('android:src="@drawable/iotools_avatar"'),
      );
    }
    await tester.runAsync(() async {
      final codec = await ui.instantiateImageCodec(bytes);
      final frame = await codec.getNextFrame();
      expect(frame.image.width, 512);
      expect(frame.image.height, 512);
      final rgba = (await frame.image.toByteData(
        format: ui.ImageByteFormat.rawRgba,
      ))!;
      double radius = 0;
      for (var y = 0; y < 512; y++)
        for (var x = 0; x < 512; x++)
          if (rgba.getUint8((y * 512 + x) * 4 + 3) > 0)
            radius = math.max(
              radius,
              math.sqrt(math.pow(x + .5 - 256, 2) + math.pow(y + .5 - 256, 2)),
            );
      expect(radius / 512 * adaptive, lessThan(33 / 72));
      expect(radius / 512 * legacy, lessThan(.5));
      frame.image.dispose();
      codec.dispose();
      final font = File(
        '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',
      );
      if (font.existsSync()) {
        final loader = FontLoader('Roboto');
        loader.addFont(
          Future.value(ByteData.sublistView(await font.readAsBytes())),
        );
        await loader.load();
      }
    });
    tester.view.physicalSize = const Size(560, 660);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final key = GlobalKey();
    Widget icon(String label, double factor, bool round) {
      final child = Container(
        width: 200,
        height: 200,
        color: const Color(0xff08141f),
        alignment: Alignment.center,
        child: SizedBox(
          width: 200 * factor,
          height: 200 * factor,
          child: Image.memory(bytes, filterQuality: FilterQuality.high),
        ),
      );
      return Column(
        children: [
          round
              ? ClipOval(child: child)
              : ClipRRect(
                  borderRadius: BorderRadius.circular(42),
                  child: child,
                ),
          const SizedBox(height: 12),
          Text(
            label,
            style: const TextStyle(color: Colors.white, fontSize: 16),
          ),
        ],
      );
    }

    await tester.pumpWidget(
      MaterialApp(
        home: RepaintBoundary(
          key: key,
          child: Scaffold(
            backgroundColor: const Color(0xff162736),
            body: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                children: [
                  const Text(
                    '用户头像 · App 图标预览',
                    style: TextStyle(fontSize: 22, color: Colors.white),
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '保留原图，帽子与主体完整显示',
                    style: TextStyle(color: Colors.white70),
                  ),
                  const SizedBox(height: 24),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceAround,
                    children: [
                      icon('自适应 · 圆形', adaptive, true),
                      icon('自适应 · 圆角方形', adaptive, false),
                    ],
                  ),
                  const SizedBox(height: 28),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceAround,
                    children: [
                      icon('传统 · 圆形', legacy, true),
                      icon('传统 · 方形', legacy, false),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
    await tester.runAsync(
      () => precacheImage(
        MemoryImage(bytes),
        tester.element(find.byType(Scaffold)),
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.runAsync(() async {
      final image =
          await (key.currentContext!.findRenderObject()!
                  as RenderRepaintBoundary)
              .toImage(pixelRatio: 1);
      final png = await image.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/flutter-evidence/launcher-avatar-preview.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(png!.buffer.asUint8List());
      image.dispose();
    });
    await tester.pumpWidget(const SizedBox());
    await tester.pump();
  });
}
