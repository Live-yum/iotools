import 'dart:convert';
import 'modbus_advanced_workflow_test.dart';
import 'modbus_interaction.dart';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/modbus/modbus_models.dart';

/// Real Flutter app → MethodChannel → JNI → shared Go → TCP wire fixture.
/// Runner must adb reverse ports 48415 (Modbus) and 48416 (metrics).
/// This synthetic loopback fixture is not evidence of physical-device support.
void registerModbusIntegrationTests() {
  registerAdvancedModbusIntegrationTests();
  testWidgets(
    'Flutter Modbus: local navigation, cancelled write, confirmed write and TCP readback',
    (tester) async {
      const engine = MethodChannelEngine(), platform = MethodChannelPlatform();
      final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
      final initialCounters = await _metrics();
      await engine.open();
      final original = mbMap(await engine.command({'op': 'config.get'}));
      const source = '''version: 1
profiles:
  local: {}
requests:
  - id: flutter-modbus-acceptance
    name: Flutter Modbus 整体验收
    protocol: modbus
    action: read-holding
    endpoint: tcp://127.0.0.1:48415
    timeout: 5s
    params:
      unit: 1
      address: 0
      count: 2
      samples: 1
      word_order: ABCD
''';
      await engine.command({'op': 'config.save', 'source': source});
      await engine.close();
      var wrote = false, restored = false;
      try {
        await tester.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await _wait(
          tester,
          () => find.text('Flutter Modbus 整体验收').evaluate().isNotEmpty,
        );
        final session = tester.widget<WorkspaceShell>(find.byType(WorkspaceShell)).session;
        _sameCounters(initialCounters, await _metrics(), 'startup');
        await _tap(tester, 'Flutter Modbus 整体验收');
        await _tap(tester, '工具');
        await _tap(tester, '寄存器、采样与高级工具');
        await _wait(
          tester,
          () => find.text('Modbus 工作区').evaluate().isNotEmpty,
        );
        _sameCounters(
          initialCounters,
          await _metrics(),
          'opening the Flutter workspace',
        );
        await _tap(tester, '地址导航');
        await _enter(tester, '地址 / 0x / 相对值 / 唯一标签', '0');
        await _enter(tester, '数量', '2');
        await _tap(tester, '仅应用窗口');
        _sameCounters(initialCounters, await _metrics(), 'local navigation');
        await _tap(tester, '操作');
        await _executeAndWait(tester, session, '预览并读取当前窗口', 'read-holding');
        final firstRead = await _metrics();
        expect(
          firstRead['modbus_reads'],
          (initialCounters['modbus_reads'] as int) + 1,
        );
        expect(firstRead['modbus_writes'], initialCounters['modbus_writes']);
        await _tap(tester, '寄存器');
        await _wait(tester, () => find.text('42').evaluate().isNotEmpty);
        expect(find.text('7'), findsWidgets);
        expect(find.text('42'), findsWidgets);
        if (Platform.isAndroid) await binding.convertFlutterSurfaceToImage();
        await tester.pump();
        await binding.takeScreenshot('flutter-modbus-01-real-tcp-read');
        await _tap(tester, '列与矩阵');
        await _tap(tester, '取消');
        await _tap(tester, '趋势');
        await _tap(tester, '关闭');
        _sameCounters(firstRead, await _metrics(), 'layout and cached trend');
        await _tap(tester, '操作');
        await _writeDialog(tester, '17');
        await _tap(tester, '取消');
        await _wait(tester, () => find.text('确认执行写操作').evaluate().isEmpty);
        _sameCounters(
          firstRead,
          await _metrics(),
          'write target review cancellation',
        );
        await _writeDialog(tester, '17');
        expect(find.textContaining('127.0.0.1:48415'), findsWidgets);
        expect(find.textContaining('registers'), findsWidgets);
        await binding.takeScreenshot('flutter-modbus-02-write-target-review');
        wrote = true;
        await _executeAndWait(tester, session, '确认执行', 'write-typed');
        final changed = await _metrics();
        expect(
          changed['modbus_writes'],
          (firstRead['modbus_writes'] as int) + 1,
        );
        expect(changed['modbus_reads'], firstRead['modbus_reads']);
        await _executeAndWait(tester, session, '预览并读取当前窗口', 'read-holding');
        await _tap(tester, '寄存器');
        await _wait(tester, () => find.text('17').evaluate().isNotEmpty);
        expect(find.text('17'), findsWidgets);
        expect(find.text('42'), findsWidgets);
        final readback = await _metrics();
        expect(
          readback['modbus_reads'],
          (firstRead['modbus_reads'] as int) + 1,
        );
        expect(readback['modbus_writes'], changed['modbus_writes']);
        await binding.takeScreenshot('flutter-modbus-03-real-tcp-readback');
        // Restore the disposable fixture through the same explicit Flutter review.
        await _tap(tester, '操作');
        await _writeDialog(tester, '7');
        await _executeAndWait(tester, session, '确认执行', 'write-typed');
        restored = true;
        final beforeLifecycle = await _metrics();
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await tester.pump(const Duration(milliseconds: 500));
        binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pump(const Duration(milliseconds: 600));
        _sameCounters(
          beforeLifecycle,
          await _metrics(),
          'lifecycle does not replay a write',
        );
        expect(tester.takeException(), isNull);
      } finally {
        if (binding.lifecycleState == AppLifecycleState.paused)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        if (binding.lifecycleState == AppLifecycleState.hidden)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        if (binding.lifecycleState == AppLifecycleState.inactive)
          binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pumpWidget(const SizedBox());
        await tester.pump(const Duration(milliseconds: 100));
        await engine.open();
        if (wrote && !restored) {
          final preview = mbMap(
            await engine.command({
              'op': 'preview',
              'request': {
                'id': 'fixture-restore',
                'protocol': 'modbus',
                'action': 'write-register',
                'endpoint': 'tcp://127.0.0.1:48415',
                'timeout': '5s',
                'params': {'unit': 1, 'address': 0, 'count': 1, 'value': 7},
              },
            }),
          );
          await engine.command({
            'op': 'run',
            'token': preview['token'],
            'confirmed': true,
          });
          await _cleanupIdle(tester, engine);
        }
        await engine.command({
          'op': 'config.save',
          'source': original['source'],
        });
        await engine.close();
      }
    },
    timeout: const Timeout(Duration(minutes: 4)),
  );
}

void main() {
  WidgetController.hitTestWarningShouldBeFatal = true;
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  registerModbusIntegrationTests();
}

Future<Map<String, dynamic>> _metrics() async {
  final client = HttpClient()..connectionTimeout = const Duration(seconds: 3);
  try {
    final request = await client.getUrl(
      Uri.parse('http://127.0.0.1:48416/metrics'),
    );
    final response = await request.close().timeout(const Duration(seconds: 3));
    expect(response.statusCode, 200);
    return mbMap(jsonDecode(await utf8.decoder.bind(response).join()));
  } finally {
    client.close(force: true);
  }
}

void _sameCounters(
  Map<String, dynamic> before,
  Map<String, dynamic> after,
  String reason,
) {
  for (final key in ['modbus_reads', 'modbus_writes']) {
    expect(after[key], before[key], reason: '$reason: $key');
  }
}

Future<void> _wait(WidgetTester tester, bool Function() ready) async {
  final end = DateTime.now().add(const Duration(seconds: 30));
  while (!ready()) {
    if (DateTime.now().isAfter(end))
      throw TestFailure('Timed out waiting for Modbus Flutter UI');
    await tester.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _tap(WidgetTester tester, String label) async {
  final finder = find.text(label).last;
  await _wait(tester, () => finder.evaluate().isNotEmpty);
  await Scrollable.ensureVisible(tester.element(finder), alignment: 0.5);
  await waitForModbusInteraction(tester, finder);
  await tester.tap(finder);
  await tester.pump(const Duration(milliseconds: 350));
}

Future<void> _enter(WidgetTester tester, String key, String value) async {
  final finder = find.byKey(ValueKey(key));
  await _wait(tester, () => finder.evaluate().isNotEmpty);
  await tester.ensureVisible(finder);
  await tester.pump();
  await tester.enterText(finder, value);
  await tester.testTextInput.receiveAction(TextInputAction.done);
  await tester.pump();
}

Future<void> _writeDialog(WidgetTester tester, String value) async {
  await _tap(tester, '写入类型数值 / 寄存器');
  await _enter(tester, '写入起始地址', '0');
  await _enter(tester, '精确数值', value);
  await _tap(tester, '预览编码与目标');
  await _wait(tester, () => find.text('确认执行写操作').evaluate().isNotEmpty);
  final countRow = find.byWidgetPredicate((widget) => widget is Column &&
      widget.children.any((child) => child is Text && child.data == '数量'));
  expect(find.descendant(of: countRow, matching: find.byWidgetPredicate(
      (widget) => widget is SelectableText && widget.data == '1')), findsOneWidget,
      reason: 'u16 写入预览必须为 1 个字，不沿用读取窗口的 2 个字');
}

Future<void> _executeAndWait(
  WidgetTester tester, AppSession session, String label, String action,
) async {
  final operation = ModbusOperationWait(session);
  try {
    await _tap(tester, label);
    await operation.wait(tester, action: action);
  } finally {
    await operation.dispose();
  }
}

Future<void> _cleanupIdle(WidgetTester tester, Engine engine) async {
  final end = DateTime.now().add(const Duration(seconds: 30));
  while (true) {
    await tester.pump(const Duration(milliseconds: 150));
    final state = mbMap(await engine.command({'op': 'state'}));
    if (state['running'] != true) {
      await tester.pump(const Duration(milliseconds: 500));
      return;
    }
    if (DateTime.now().isAfter(end))
      throw TestFailure('Modbus fixture operation did not finish');
  }
}
