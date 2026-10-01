import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/features/opcua/opcua_models.dart';

/// Called by the single Actions app_test.dart target. Uses the real platform
/// channel/JNI/Go engine and disposable OPC fixture, never a fake engine.
bool _surfaceConverted = false;
void registerOpcuaIntegrationTests() {
  testWidgets(
    'Flutter OPC whole app: discovery, typed writes, methods, subscriptions and lifecycle',
    (tester) async {
      _surfaceConverted = false;
      const engine = MethodChannelEngine();
      const platform = MethodChannelPlatform();
      final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
      final ready = await _fixture('/ready'),
          before = await _fixture('/metrics');
      expect(ready['loopback_only'], true);
      final object = '${ready['object']}',
          temperature = '${ready['temperature']}',
          pressure = '${ready['pressure']}',
          method = '${ready['method']}';
      await engine.open();
      final original = uaMap(await engine.command({'op': 'config.get'}));
      const source = '''version: 1
profiles:
  local: {}
requests:
  - id: flutter-opc-acceptance
    name: Flutter OPC 整体验收
    protocol: opcua
    action: browse
    endpoint: opc.tcp://127.0.0.1:48410
    timeout: 10s
    params:
      node_id: ns=1;i=85
      security_policy: None
      security_mode: None
      allow_insecure: true
''';
      await engine.command({'op': 'config.save', 'source': source});
      await engine.close();
      try {
        await tester.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await _wait(
          tester,
          () => find.text('Flutter OPC 整体验收').evaluate().isNotEmpty,
        );
        _sameCounters(
          'startup remains local',
          before,
          await _fixture('/metrics'),
        );
        await _tapText(tester, 'Flutter OPC 整体验收');
        await _tapText(tester, '工具');
        await _tapText(tester, '节点浏览、属性、方法与订阅');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-selected-node'))
              .evaluate()
              .isNotEmpty,
        );
        _sameCounters(
          'opening Flutter workspace remains local',
          before,
          await _fixture('/metrics'),
        );
        await _tap(tester, 'ua-tab-discovery');
        await _tap(tester, 'ua-refresh');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-select-endpoint'))
              .evaluate()
              .isNotEmpty,
        );
        await _idle(tester, engine);
        final discovered = await _fixture('/metrics');
        expect(
          discovered['discoveries'],
          greaterThan(before['discoveries'] as int),
        );
        await _screenshot(tester, binding, 'flutter-opc-01-discovery');
        await _tap(tester, 'ua-select-endpoint');
        final allow = find.widgetWithText(
          SwitchListTile,
          '明确允许 None 无加密（仅可信测试环境）',
        );
        await tester.ensureVisible(allow);
        await tester.tap(allow);
        await tester.pump();
        await _tap(tester, 'ua-apply-connection');
        _sameCounters(
          'endpoint selection only changes draft',
          discovered,
          await _fixture('/metrics'),
        );
        await _jump(tester, object);
        await _tap(tester, 'ua-tab-browse');
        await _tap(tester, 'ua-refresh');
        await _wait(
          tester,
          () => find
              .byKey(ValueKey('ua-node-$temperature'))
              .evaluate()
              .isNotEmpty,
        );
        await _idle(tester, engine);
        await _screenshot(tester, binding, 'flutter-opc-02-browse');
        final browsed = await _fixture('/metrics');
        await _tap(tester, 'ua-node-$temperature');
        await _tap(tester, 'ua-back');
        await _tap(tester, 'ua-forward');
        _sameCounters(
          'cached navigation does not read',
          browsed,
          await _fixture('/metrics'),
        );
        await _tap(tester, 'ua-tab-attributes');
        await _tap(tester, 'ua-refresh');
        await _idle(tester, engine);
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-attribute-Value'))
              .evaluate()
              .isNotEmpty,
        );
        expect(
          find.byKey(const ValueKey('ua-attribute-ValueRank')),
          findsOneWidget,
        );
        await _screenshot(tester, binding, 'flutter-opc-03-attributes');
        await _tap(tester, 'ua-write');
        await _enter(tester, 'ua-write-value-Int32', '99');
        final beforeCancel = await _fixture('/metrics');
        await _tap(tester, 'ua-preview-write');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-confirm-run'))
              .evaluate()
              .isNotEmpty,
        );
        await _screenshot(tester, binding, 'flutter-opc-04-write-review');
        await _tap(tester, 'ua-review-cancel');
        _sameCounters(
          'cancelled typed write is zero protocol activity',
          beforeCancel,
          await _fixture('/metrics'),
        );
        await _enter(tester, 'ua-write-value-Int32', '2147483648');
        await _tap(tester, 'ua-preview-write');
        expect(find.textContaining('Int32 范围'), findsOneWidget);
        _sameCounters(
          'invalid typed write is rejected locally',
          beforeCancel,
          await _fixture('/metrics'),
        );
        await _enter(tester, 'ua-write-value-Int32', '99');
        await _tap(tester, 'ua-preview-write');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-confirm-run'))
              .evaluate()
              .isNotEmpty,
        );
        await _tap(tester, 'ua-confirm-run');
        await _idle(tester, engine);
        expect(
          (await _fixture('/metrics'))['writes'],
          (before['writes'] as int) + 1,
        );
        await _tap(tester, 'ua-refresh');
        await _idle(tester, engine);
        await _wait(
          tester,
          () => _selectable(tester, 'ua-attribute-Value-value') == '99',
        );
        await tester.ensureVisible(
          find.byKey(const ValueKey('ua-attribute-Value-value')),
        );
        await _screenshot(tester, binding, 'flutter-opc-05-write-readback-99');
        await _tap(tester, 'ua-back');
        await _tap(tester, 'ua-tab-browse');
        await _tap(tester, 'ua-method-$method');
        await _tap(tester, 'ua-fetch-signature');
        await _idle(tester, engine);
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-open-method-call'))
              .evaluate()
              .isNotEmpty,
        );
        expect((await _fixture('/metrics'))['calls'], before['calls']);
        await _tap(tester, 'ua-open-method-call');
        await _enter(tester, 'ua-argument-0-Int32', '6');
        final beforeCall = await _fixture('/metrics');
        await _tap(tester, 'ua-preview-call');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-confirm-run'))
              .evaluate()
              .isNotEmpty,
        );
        await _tap(tester, 'ua-review-cancel');
        _sameCounters(
          'signature and cancelled call never invoke',
          beforeCall,
          await _fixture('/metrics'),
        );
        await _enter(tester, 'ua-argument-0-Int32', '2147483648');
        await _tap(tester, 'ua-preview-call');
        expect(find.textContaining('Int32 范围'), findsOneWidget);
        _sameCounters(
          'invalid method argument rejected locally',
          beforeCall,
          await _fixture('/metrics'),
        );
        await _enter(tester, 'ua-argument-0-Int32', '6');
        await _tap(tester, 'ua-preview-call');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-confirm-run'))
              .evaluate()
              .isNotEmpty,
        );
        await _tap(tester, 'ua-confirm-run');
        await _idle(tester, engine);
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-method-outputs'))
              .evaluate()
              .isNotEmpty,
        );
        expect(_selectable(tester, 'ua-method-outputs'), '[12]');
        expect(
          (await _fixture('/metrics'))['calls'],
          (before['calls'] as int) + 1,
        );
        await tester.ensureVisible(
          find.byKey(const ValueKey('ua-method-outputs')),
        );
        await _screenshot(tester, binding, 'flutter-opc-06-method-double-6-12');
        await _tapText(tester, '关闭');
        await _jump(tester, temperature);
        await _tap(tester, 'ua-tab-subscriptions');
        await _addSubscription(tester);
        await _subscriptions(tester, engine, 1);
        await _jump(tester, pressure);
        await _addSubscription(tester);
        await _subscriptions(tester, engine, 2);
        await _tap(tester, 'ua-refresh-subscriptions');
        await _screenshot(tester, binding, 'flutter-opc-07-two-subscriptions');
        await _tap(tester, 'ua-tab-attributes');
        await _tap(tester, 'ua-refresh');
        await _idle(tester, engine);
        await _subscriptions(tester, engine, 2);
        final subs = await engine.command({'op': 'subscriptions.list'}) as List;
        final target = subs
            .map(uaMap)
            .firstWhere(
              (s) =>
                  (s['node_ids'] as List).contains(temperature) &&
                  s['status'] == 'running',
            );
        await _tap(tester, 'ua-tab-subscriptions');
        await _tap(tester, 'ua-refresh-subscriptions');
        await _tap(tester, 'ua-stop-${target['id']}');
        await _subscriptions(tester, engine, 1);
        final remaining =
            (await engine.command({'op': 'subscriptions.list'}) as List)
                .map(uaMap)
                .where((s) => s['status'] == 'running')
                .single;
        expect(remaining['node_ids'], [pressure]);
        // Resize the Flutter surface to exercise responsive layout; this is not
        // reported as a physical-device rotation test.
        await _jump(tester, object);
        await _tap(tester, 'ua-tab-browse');
        final beforeResize = await _fixture('/metrics');
        await binding.setSurfaceSize(const Size(1000, 700));
        await tester.pump(const Duration(milliseconds: 500));
        _sameCounters(
          'responsive resize does not replay requests',
          beforeResize,
          await _fixture('/metrics'),
        );
        await _screenshot(tester, binding, 'flutter-opc-08-wide-workspace');
        await binding.setSurfaceSize(null);
        // Drive Flutter's lifecycle callback through the real platform pause API.
        // The separate runner may also exercise actual OS background/resume.
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await _subscriptions(tester, engine, 0);
        final stopped = await _fixture('/metrics');
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pump(const Duration(milliseconds: 600));
        await _subscriptions(tester, engine, 0);
        _sameCounters(
          'resume does not restart subscriptions or replay mutations',
          stopped,
          await _fixture('/metrics'),
        );
        expect(
          (await _fixture('/metrics'))['writes'],
          (before['writes'] as int) + 1,
        );
        expect(
          (await _fixture('/metrics'))['calls'],
          (before['calls'] as int) + 1,
        );
        await _screenshot(
          tester,
          binding,
          'flutter-opc-09-background-no-replay',
        );
      } finally {
        if (binding.lifecycleState == AppLifecycleState.paused)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        if (binding.lifecycleState == AppLifecycleState.hidden)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        if (binding.lifecycleState == AppLifecycleState.inactive)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await binding.setSurfaceSize(null);
        await tester.pumpWidget(const SizedBox());
        await engine.open();
        await engine.command({
          'op': 'config.save',
          'source': original['source'],
        });
        await engine.close();
      }
    },
    timeout: const Timeout(Duration(minutes: 5)),
  );
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  registerOpcuaIntegrationTests();
}

Future<UaMap> _fixture(String path) async {
  final client = HttpClient()..connectionTimeout = const Duration(seconds: 3);
  try {
    final req = await client.getUrl(Uri.parse('http://127.0.0.1:48411$path'));
    final reply = await req.close().timeout(const Duration(seconds: 3));
    expect(reply.statusCode, 200);
    return uaMap(jsonDecode(await utf8.decoder.bind(reply).join()));
  } finally {
    client.close(force: true);
  }
}

void _sameCounters(String reason, UaMap a, UaMap b) {
  for (final key in ['discoveries', 'browses', 'reads', 'writes', 'calls']) {
    expect(b[key], a[key], reason: '$reason: $key');
  }
}

Future<void> _wait(WidgetTester tester, bool Function() predicate) async {
  final end = DateTime.now().add(const Duration(seconds: 35));
  while (!predicate()) {
    if (DateTime.now().isAfter(end))
      throw TestFailure('Timed out waiting for Flutter OPC UI');
    await tester.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _tap(WidgetTester tester, String key) async {
  final f = find.byKey(ValueKey(key));
  await _wait(tester, () => f.evaluate().isNotEmpty);
  await tester.ensureVisible(f);
  await tester.pump();
  await tester.tap(f);
  await tester.pump(const Duration(milliseconds: 300));
}

Future<void> _tapText(WidgetTester tester, String text) async {
  final f = find.text(text);
  await _wait(tester, () => f.evaluate().isNotEmpty);
  await tester.ensureVisible(f.last);
  await tester.pump();
  await tester.tap(f.last);
  await tester.pump(const Duration(milliseconds: 300));
}

Future<void> _enter(WidgetTester tester, String key, String value) async {
  final f = find.byKey(ValueKey(key));
  await tester.ensureVisible(f);
  await tester.enterText(f, value);
  await tester.testTextInput.receiveAction(TextInputAction.done);
  await tester.pump();
}

String _selectable(WidgetTester tester, String key) {
  final f = find.byKey(ValueKey(key));
  return f.evaluate().isEmpty
      ? ''
      : tester.widget<SelectableText>(f).data ?? '';
}

Future<void> _jump(WidgetTester tester, String node) async {
  await _tap(tester, 'ua-jump');
  await _enter(tester, 'ua-jump-input', node);
  await _tap(tester, 'ua-form-submit');
}

Future<void> _idle(WidgetTester tester, Engine engine) async {
  final end = DateTime.now().add(const Duration(seconds: 35));
  while (true) {
    await tester.pump(const Duration(milliseconds: 150));
    final state = uaMap(await engine.command({'op': 'state'}));
    final status = find.byKey(const ValueKey('ua-status'));
    final text = status.evaluate().isEmpty
        ? ''
        : tester.widget<Text>(status).data ?? '';
    if (state['running'] != true &&
        (text.startsWith('任务完成') ||
            text.startsWith('任务失败') ||
            text.startsWith('任务已取消'))) {
      await tester.pump(const Duration(milliseconds: 400));
      return;
    }
    if (DateTime.now().isAfter(end))
      throw TestFailure('OPC foreground did not stop');
  }
}

Future<void> _addSubscription(WidgetTester tester) async {
  await _tap(tester, 'ua-add-subscription');
  await _enter(tester, 'ua-sub-interval', '100');
  await _tap(tester, 'ua-preview-subscribe');
  await _wait(
    tester,
    () => find.byKey(const ValueKey('ua-confirm-run')).evaluate().isNotEmpty,
  );
  await _tap(tester, 'ua-confirm-run');
}

Future<void> _subscriptions(
  WidgetTester tester,
  Engine engine,
  int count,
) async {
  final end = DateTime.now().add(const Duration(seconds: 25));
  while (true) {
    final all = (await engine.command({'op': 'subscriptions.list'}) as List)
        .map(uaMap)
        .where((r) => r['status'] == 'running')
        .toList();
    if (all.length == count &&
        (count == 0 || all.every((r) => r['last_event'] != null)))
      return;
    if (DateTime.now().isAfter(end))
      throw TestFailure('Expected $count notified subscriptions, got $all');
    await tester.pump(const Duration(milliseconds: 150));
  }
}

Future<void> _screenshot(
  WidgetTester tester,
  IntegrationTestWidgetsFlutterBinding binding,
  String name,
) async {
  if (Platform.isAndroid && !_surfaceConverted) {
    await binding.convertFlutterSurfaceToImage();
    _surfaceConverted = true;
  }
  await tester.pump();
  await binding.takeScreenshot(name);
}
