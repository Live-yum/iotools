import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/offline_fonts.dart';
import 'action_interaction.dart';
import 'app_test.dart' show waitFor, tapKey, tapText, openRequest;
import 'runtime_adapter.dart';

void main() {
  WidgetController.hitTestWarningShouldBeFatal = true;
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('actual native desktop UI: exact HTTP write, cancel, hidden and no replay', (tester) async {
    await loadOfflineFonts();
    final runtime = protocolTestRuntime();
    final engine = runtime.engine, platform = runtime.platform;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final received = <String>[];
    final subscription = server.listen((request) async {
      received.add(await utf8.decoder.bind(request).join());
      request.response.headers.contentType = ContentType('text', 'plain', charset: 'utf-8');
      request.response.write('原生桌面真实响应😀');
      await request.response.close();
    });
    try {
      final settings = mapOf(await platform.invoke('settings.get'));
      expect(settings['platform'], Platform.operatingSystem);
      expect(mapOf(settings['capabilities'])['native_protocols'], true);
      expect(mapOf(settings['capabilities'])['usb'], false);
      await engine.open();
      await engine.command({'op': 'config.save', 'source': '''version: 1
requests:
  - id: exact-desktop
    name: 桌面精确 HTTP 写入
    protocol: http
    action: POST
    endpoint: http://127.0.0.1:${server.port}/echo
    timeout: 5s
    params:
      json:
        number: 18446744073709551615
        string: "18446744073709551615"
'''});
      await engine.close();
      await tester.pumpWidget(protocolTestApp(engine, platform));
      await waitFor(tester, () => find.text('桌面精确 HTTP 写入').evaluate().isNotEmpty);
      await takeProtocolScreenshot(tester, binding, 'desktop-01-home');
      await openRequest(tester, '桌面精确 HTTP 写入');
      await tapKey(tester, 'run_request');
      await waitFor(tester, () => find.byKey(const ValueKey('confirm_action')).evaluate().isNotEmpty);
      final review = tester.widget<SelectableText>(find.byKey(const ValueKey('review_json'))).data!;
      expect(review, contains('"number":18446744073709551615'));
      expect(review, contains('"string":"18446744073709551615"'));
      await takeProtocolScreenshot(tester, binding, 'desktop-02-exact-review');
      expect(received, isEmpty);
      await tapText(tester, '取消');
      expect(received, isEmpty);
      await tapKey(tester, 'run_request');
      await waitFor(tester, () => find.byKey(const ValueKey('confirm_action')).evaluate().isNotEmpty);
      pauseTestLifecycle(binding);
      await waitForBackgroundCondition(tester, () async => mapOf(await engine.command({'op': 'state'}))['paused'] == true);
      expect(received, isEmpty);
      resumeTestLifecycle(binding);
      await waitFor(tester, () => find.byKey(const ValueKey('confirm_action')).evaluate().isEmpty);
      await tapKey(tester, 'run_request');
      await tapKey(tester, 'confirm_action');
      await waitFor(tester, () => find.text('已完成').evaluate().isNotEmpty);
      expect(received, hasLength(1));
      expect(received.single, contains('"number":18446744073709551615'));
      expect(received.single, contains('"string":"18446744073709551615"'));
      expect(find.textContaining('原生桌面真实响应'), findsWidgets);
      await takeProtocolScreenshot(tester, binding, 'desktop-03-real-response');
      pauseTestLifecycle(binding);
      await waitForBackgroundCondition(tester, () async => mapOf(await engine.command({'op': 'state'}))['paused'] == true);
      resumeTestLifecycle(binding);
      await tester.pump(const Duration(milliseconds: 300));
      expect(received, hasLength(1));
      expect(mapOf(await engine.command({'op': 'state'}))['running'], false);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      print('NATIVE_DESKTOP_SMOKE_PASS ${Platform.operatingSystem} exact-write=1 cancel=0 hidden-replay=0');
    } finally {
      resumeTestLifecycle(binding);
      await engine.close();
      await subscription.cancel();
      await server.close(force: true);
    }
  });
}
