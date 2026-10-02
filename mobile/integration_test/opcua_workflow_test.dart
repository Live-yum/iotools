import 'runtime_adapter.dart';
import 'action_interaction.dart';
import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/features/opcua/opcua_models.dart';

/// Called by the isolated Actions opcua_workflow_test.dart target. Uses the real platform
/// channel/JNI/Go engine and disposable OPC fixture, never a fake engine.
bool _surfaceConverted = false;
final _progressClock = Stopwatch();
void _stage(String name) =>
    debugPrint('OPC_STAGE ${_progressClock.elapsedMilliseconds}ms $name');
void registerOpcuaIntegrationTests() {
  testWidgets(
    'Flutter OPC whole app: discovery, typed writes, methods, subscriptions and lifecycle',
    (tester) async {
      _surfaceConverted = false;
      _progressClock
        ..reset()
        ..start();
      _stage('start');
      final runtime = protocolTestRuntime();
      final engine = runtime.engine, platform = runtime.platform;
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
          protocolTestApp(engine, platform),
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
        await _pump(tester);
        await tester.tap(allow);
        await _pump(tester);
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
        await revealOpcuaAttribute(tester, 'ua-attribute-Value');
        await _wait(
          tester,
          () => find
              .byKey(const ValueKey('ua-attribute-Value'))
              .evaluate()
              .isNotEmpty,
        );
        await revealOpcuaAttribute(tester, 'ua-attribute-ValueRank');
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
        await _wait(
          tester,
          () => find.textContaining('Int32 范围').evaluate().isNotEmpty,
        );
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
        await revealOpcuaAttribute(
          tester,
          'ua-attribute-Value-value',
          fromStart: true,
        );
        await _wait(
          tester,
          () => _selectable(tester, 'ua-attribute-Value-value') == '99',
        );
        await tester.ensureVisible(
          find.byKey(const ValueKey('ua-attribute-Value-value')),
        );
        await _pump(tester);
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
        await _wait(
          tester,
          () => find.textContaining('Int32 范围').evaluate().isNotEmpty,
        );
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
        await _pump(tester);
        await _screenshot(tester, binding, 'flutter-opc-06-method-double-6-12');
        await _tapText(tester, '关闭');
        await _jump(tester, temperature);
        await _tap(tester, 'ua-tab-subscriptions');
        _stage('first-subscription-review');
        await _addSubscription(tester);
        _stage('first-subscription-confirmed');
        await _subscriptions(tester, engine, 1);
        _stage('first-subscription-notified');
        await _jump(tester, pressure);
        _stage('second-subscription-review');
        await _addSubscription(tester);
        _stage('second-subscription-confirmed');
        await _subscriptions(tester, engine, 2);
        _stage('second-subscription-notified');
        await _tap(tester, 'ua-refresh-subscriptions');
        await _screenshot(tester, binding, 'flutter-opc-07-two-subscriptions');
        await _tap(tester, 'ua-tab-attributes');
        await _tap(tester, 'ua-refresh');
        await _idle(tester, engine);
        await _subscriptions(tester, engine, 2);
        final subs =
            await engine
                    .command({'op': 'subscriptions.list'})
                    .timeout(const Duration(seconds: 10))
                as List;
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
            (await engine
                        .command({'op': 'subscriptions.list'})
                        .timeout(const Duration(seconds: 10))
                    as List)
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
        await _pump(tester, const Duration(milliseconds: 500));
        _sameCounters(
          'responsive resize does not replay requests',
          beforeResize,
          await _fixture('/metrics'),
        );
        await _screenshot(tester, binding, 'flutter-opc-08-wide-workspace');
        await binding.setSurfaceSize(null);
        // Drive Flutter's lifecycle callback through the real platform pause API.
        // The separate runner may also exercise actual OS background/resume.
        _stage('background-stop-begin');
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await waitForBackgroundCondition(tester, () async {
          final state = uaMap(
            await engine
                .command({'op': 'state'})
                .timeout(const Duration(seconds: 10)),
          );
          final subscriptions = await engine.command({
            'op': 'subscriptions.list',
          });
          return state['paused'] == true &&
              (subscriptions as List)
                  .map(uaMap)
                  .every((row) => row['status'] != 'running');
        });
        _stage('background-stop-acknowledged');
        final stopped = await _fixture('/metrics');
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await _pump(tester, const Duration(milliseconds: 600));
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
        _stage('cleanup-begin');
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
        _stage('cleanup-complete');
      }
    },
    // API29 software emulation took >5 minutes before subscriptions in the
    // recorded run, while every protocol operation retained its short limit.
    // Keep a finite whole-flow budget without interrupting its bounded cleanup.
    timeout: const Timeout(Duration(minutes: 8)),
  );
}

void main() {
  WidgetController.hitTestWarningShouldBeFatal = true;
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  registerOpcuaIntegrationTests();
}

Future<UaMap> _fixture(String path) async {
  final client = HttpClient()..connectionTimeout = const Duration(seconds: 3);
  try {
    final req = await client.getUrl(Uri.parse('http://127.0.0.1:48411$path'));
    final reply = await req.close().timeout(const Duration(seconds: 3));
    expect(reply.statusCode, 200);
    return uaMap(
      jsonDecode(
        await utf8.decoder
            .bind(reply)
            .join()
            .timeout(const Duration(seconds: 3)),
      ),
    );
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
      throw TestFailure(
        'Timed out waiting for Flutter OPC UI; visible=${find.byType(Text).evaluate().map((e) => (e.widget as Text).data).whereType<String>().join(" | ")}',
      );
    await _pump(tester, const Duration(milliseconds: 100));
  }
}

/// Reveal a lazy attribute using the same drag gestures as the actual UI.
/// Kept public so the full 27-attribute widget fixture exercises this helper.
Future<void> revealOpcuaAttribute(
  WidgetTester tester,
  String key, {
  bool fromStart = false,
}) async {
  final end = DateTime.now().add(const Duration(seconds: 35));
  final target = find.byKey(ValueKey(key));
  final body = find.byKey(const ValueKey('ua-scroll-attributes'));
  await tester.ensureVisible(body);
  await _pump(tester);
  final scrollable = find
      .descendant(of: body, matching: find.byType(Scrollable))
      .first;
  final position = tester.state<ScrollableState>(scrollable).position;
  // A refresh can rebuild the list at the top or retain its old offset. Reset
  // only after refresh; sequential targets continue from the current viewport.
  if (fromStart && target.evaluate().isEmpty && position.pixels > 0) {
    await tester.drag(
      scrollable,
      Offset(0, position.pixels + position.viewportDimension),
    );
    await _pump(tester, const Duration(milliseconds: 50));
  }
  final viewport = tester.renderObject<RenderViewport>(
    find.descendant(of: body, matching: find.byType(Viewport)).first,
  );
  final cache =
      (viewport.cacheExtent ?? RenderAbstractViewport.defaultCacheExtent) *
      (viewport.cacheExtentStyle == CacheExtentStyle.viewport
          ? position.viewportDimension
          : 1);
  // Adjacent viewport-plus-cache windows overlap, so even a short card cannot
  // be skipped. A large non-animated drag avoids many tiny physical frames.
  final delta = (position.viewportDimension + 2 * cache) * .9;
  for (var scrolls = 0; scrolls < 60; scrolls++) {
    if (DateTime.now().isAfter(end)) {
      throw TestFailure('Timed out scrolling to OPC attribute $key');
    }
    if (target.evaluate().isNotEmpty) {
      await tester.ensureVisible(target);
      await _pump(tester);
      return;
    }
    await tester.drag(scrollable, Offset(0, -delta));
    await _pump(tester, const Duration(milliseconds: 50));
  }
  throw TestFailure('OPC attribute $key was not found after 60 scrolls');
}

Future<void> _tap(WidgetTester tester, String key) async {
  final f = find.byKey(ValueKey(key));
  await _wait(tester, () => f.evaluate().isNotEmpty);
  await tapReadyControl(tester, f).timeout(const Duration(seconds: 25));
  await _pump(tester, const Duration(milliseconds: 100));
}

Future<void> _tapText(WidgetTester tester, String text) async {
  final f = find.text(text);
  await _wait(tester, () => f.evaluate().isNotEmpty);
  await tapReadyControl(tester, f.last).timeout(const Duration(seconds: 25));
  await _pump(tester, const Duration(milliseconds: 300));
}

Future<void> _enter(WidgetTester tester, String key, String value) async {
  await enterReadyText(
    tester,
    find.byKey(ValueKey(key)),
    value,
  ).timeout(const Duration(seconds: 30));
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
    await _pump(tester, const Duration(milliseconds: 150));
    final state = uaMap(
      await engine
          .command({'op': 'state'})
          .timeout(const Duration(seconds: 10)),
    );
    final status = find.byKey(const ValueKey('ua-status'));
    final text = status.evaluate().isEmpty
        ? ''
        : tester.widget<Text>(status).data ?? '';
    if (state['running'] != true &&
        (text.startsWith('任务完成') ||
            text.startsWith('任务失败') ||
            text.startsWith('任务已取消'))) {
      await _pump(tester, const Duration(milliseconds: 400));
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
    final all =
        (await engine
                    .command({'op': 'subscriptions.list'})
                    .timeout(const Duration(seconds: 10))
                as List)
            .map(uaMap)
            .where((r) => r['status'] == 'running')
            .toList();
    if (all.length == count &&
        (count == 0 || all.every((r) => r['last_event'] != null)))
      return;
    if (DateTime.now().isAfter(end))
      throw TestFailure('Expected $count notified subscriptions, got $all');
    await _pump(tester, const Duration(milliseconds: 150));
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
  _stage('capture-begin:$name');
  await _pump(tester);
  await takeProtocolScreenshot(tester, binding, name).timeout(const Duration(seconds: 30));
  _stage('capture-complete:$name');
  debugPrint('OPC_METRICS $name ${jsonEncode(await _fixture('/metrics'))}');
}

Future<void> _pump(WidgetTester tester, [Duration? duration]) => tester
    .pump(duration)
    .timeout(
      const Duration(seconds: 15),
      onTimeout: () => throw TestFailure(
        'OPC frame did not complete within 15 seconds at ${_progressClock.elapsedMilliseconds}ms',
      ),
    );
