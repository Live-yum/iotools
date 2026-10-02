import 'dart:io';
import 'dart:convert';
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/runtime.dart';
import 'package:iotools_mobile/core/platform/native/native_engine.dart';
import 'package:iotools_mobile/core/platform/native/native_platform.dart';

final _nativeSurface = GlobalKey();
AppRuntime protocolTestRuntime() {
  if (Platform.isAndroid) {
    return const AppRuntime(engine: MethodChannelEngine(), platform: MethodChannelPlatform());
  }
  final root = Directory.systemTemp.createTempSync('iotools-native-ui-');
  final platform = NativePlatformServices(rootDirectory: root.path);
  final engine = NativeFfiEngine(root: platform.privateRoot);
  addTearDown(() async {
    await engine.close();
    if (await root.exists()) await root.delete(recursive: true);
  });
  return AppRuntime(engine: engine, platform: platform);
}
Widget protocolTestApp(Engine engine, PlatformServices platform) {
  final app = IotoolsApp(engine: engine, platform: platform);
  return Platform.isAndroid ? app : RepaintBoundary(key: _nativeSurface, child: app);
}
Future<void> takeProtocolScreenshot(WidgetTester tester, IntegrationTestWidgetsFlutterBinding binding, String name) async {
  if (Platform.isAndroid || Platform.isIOS) {
    await binding.takeScreenshot(name);
    return;
  }
  await tester.pump();
  final boundary = _nativeSurface.currentContext!.findRenderObject()! as RenderRepaintBoundary;
  final image = await boundary.toImage(pixelRatio: 1);
  try {
    final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
    if (Platform.isMacOS) {
      // A sandboxed app cannot write screenshots into the host checkout.
      // Return image bytes through the integration-test driver instead.
      final report = binding.reportData ??= <String, dynamic>{};
      final screenshots = report['nativeScreenshots'] ??= <String, String>{};
      screenshots[name] = base64Encode(bytes!.buffer.asUint8List());
      return;
    }
    final root = Directory('build/flutter-evidence')..createSync(recursive: true);
    final safe = name.replaceAll(RegExp(r'[^a-zA-Z0-9_-]'), '_');
    await File('${root.path}/native-$safe.png').writeAsBytes(bytes!.buffer.asUint8List());
  } finally { image.dispose(); }
}
