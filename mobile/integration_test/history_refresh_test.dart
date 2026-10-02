import 'dart:async';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/offline_fonts.dart';
import 'app_test.dart' show waitFor, tapKey, openRequest;
import 'runtime_adapter.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('HTTP history defaults on, updates while visible, and persists after restart', (tester) async {
    await loadOfflineFonts();
    final runtime = protocolTestRuntime();
    final engine = runtime.engine, platform = runtime.platform;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final received = Completer<void>();
    final release = Completer<void>();
    final serving = server.listen((request) async {
      if (!received.isCompleted) received.complete();
      await release.future;
      request.response.write('history-fixture-response');
      await request.response.close();
    });
    try {
      expect(mapOf(await platform.invoke('settings.get'))['history'], true);
      await engine.open();
      await engine.command({'op': 'config.save', 'source': '''version: 1
requests:
  - id: history-live
    name: 历史自动刷新
    protocol: http
    action: GET
    endpoint: http://127.0.0.1:${server.port}/history
    timeout: 30s
'''});
      await engine.close();
      await tester.pumpWidget(protocolTestApp(engine, platform));
      await waitFor(tester, () => find.text('历史自动刷新').evaluate().isNotEmpty);
      await openRequest(tester, '历史自动刷新');
      await tapKey(tester, 'run_request');
      await waitFor(tester, () => received.isCompleted);
      await tapKey(tester, 'nav_history');
      await waitFor(tester, () => find.text('暂无执行历史').evaluate().isNotEmpty);
      release.complete();
      await waitFor(tester, () => find.text('GET history-live').evaluate().isNotEmpty);
      final rows = rowsOf(await engine.command({'op': 'history.list'}));
      expect(rows, hasLength(1));
      expect(rows.single['status'].toString(), '200');
      await takeProtocolScreenshot(tester, binding, 'history-completed-visible');
      await tapKey(tester, 'nav_requests');
      await tapKey(tester, 'nav_history');
      await waitFor(tester, () => find.text('GET history-live').evaluate().isNotEmpty);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      await engine.close();
      await engine.open();
      expect(rowsOf(await engine.command({'op': 'history.list'})), hasLength(1));
      await platform.invoke('settings.save', {'history': false});
      expect(mapOf(await platform.invoke('settings.get'))['history'], false);
      print('HTTP_HISTORY_UI_PASS ${Platform.operatingSystem} default-on visible-completion reentry restart explicit-off');
    } finally {
      if (!release.isCompleted) release.complete();
      await engine.close();
      await serving.cancel();
      await server.close(force: true);
    }
  });
}
