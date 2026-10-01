import 'dart:convert';
import 'modbus_interaction.dart';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/features/modbus/modbus_models.dart';

/// Second acceptance batch. Every device operation is initiated from the actual
/// Flutter form/review, then independently observed at the synthetic TCP fixture.
/// Direct engine calls are confined to setup, status and best-effort restoration.
void registerAdvancedModbusIntegrationTests() {
  testWidgets(
    'Flutter Modbus advanced: FC23, identification, PDU, sweep, probe, audit and loopback controller',
    (tester) async {
      const engine = MethodChannelEngine(), platform = MethodChannelPlatform();
      final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
      final settings = mbMap(await platform.invoke('settings.get'));
      await platform.invoke('settings.save', {
        'readOnly': false,
        'history': false,
        'collection': 'iotools.yaml',
      });
      await engine.open();
      final original = mbMap(await engine.command({'op': 'config.get'}));
      const source = '''version: 1
profiles:
  local: {}
requests:
  - id: flutter-modbus-advanced
    name: Flutter Modbus 高级整体验收
    protocol: modbus
    action: read-holding
    endpoint: tcp://127.0.0.1:48415
    timeout: 10s
    params:
      unit: 1
      address: 0
      count: 2
      samples: 1
      word_order: ABCD
''';
      await engine.command({'op': 'config.save', 'source': source});
      await engine.close();
      try {
        await tester.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await _wait(
          tester,
          () => find.text('Flutter Modbus 高级整体验收').evaluate().isNotEmpty,
        );
        await _tap(tester, 'Flutter Modbus 高级整体验收');
        await _tap(tester, '工具');
        await _tap(tester, '寄存器、采样与高级工具');
        await _tap(tester, '操作');

        // M9: distinct read/write ranges, cancel first, then one FC23 transaction.
        var before = await _metrics();
        await _fc23(tester);
        await _tap(tester, '取消');
        _same(before, await _metrics(), 'FC23 cancelled target review');
        await _fc23(tester);
        await _tap(tester, '确认执行');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 1,
          writes: 1,
          reason: 'one FC23 transaction',
        );
        await _tap(tester, '寄存器');
        await _wait(tester, () => find.text('23').evaluate().isNotEmpty);
        expect(find.text('23'), findsWidgets);
        expect(find.text('42'), findsWidgets);
        await _tap(tester, '操作');

        // M15: native object list receives the FC43 fixture's real object values.
        before = await _metrics();
        await _tap(tester, '读取设备标识');
        await _tap(tester, '预览读取');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 1,
          writes: 0,
          reason: 'FC43 device identification',
        );
        await _tap(tester, '会话');
        await _reveal(tester, find.text('iotools fixture'));
        expect(find.text('local-loopback'), findsOneWidget);
        expect(find.text('1.0'), findsOneWidget);
        await _tap(tester, '操作');

        // M16: read-PDU roundtrip, forbidden function in read mode, then raw write.
        before = await _metrics();
        await _raw(tester, write: false, hex: '0300000002', count: '2');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 1,
          writes: 0,
          reason: 'FC03 raw read',
        );
        await _tap(tester, '会话');
        await _reveal(tester, find.text('请求 PDU（hex）'));
        expect(find.text('0300000002'), findsOneWidget);
        expect(find.text('03040017002a'), findsOneWidget);
        await _tap(tester, '操作');
        before = await _metrics();
        await _raw(tester, write: false, hex: '0600000007', count: '1');
        await _wait(
          tester,
          () => find
              .textContaining('read-raw only permits')
              .evaluate()
              .isNotEmpty,
        );
        _same(
          before,
          await _metrics(),
          'write function cannot masquerade as raw read',
        );
        await _raw(tester, write: true, hex: '0600000007', count: '1');
        await _tap(tester, '取消');
        _same(before, await _metrics(), 'raw write target cancellation');
        await _raw(tester, write: true, hex: '0600000007', count: '1');
        await _tap(tester, '确认执行');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 0,
          writes: 1,
          reason: 'confirmed FC06 raw write',
        );

        // M17: exactly two single-word reads in a one-cycle, two-address sweep.
        before = await _metrics();
        await _tap(tester, '范围扫描 / 搜索');
        await _enter(tester, '起始地址', '0');
        await _enter(tester, '结束地址（含）', '1');
        await _enter(tester, '每批数量', '1');
        await _enter(tester, '扫描周期', '1');
        await _tap(tester, '预览完整范围');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 2,
          writes: 0,
          reason: 'bounded two-address sweep',
        );
        await _tap(tester, '会话');
        await _reveal(tester, find.text('sweep-progress').first);
        expect(find.text('failed_batches'), findsWidgets);
        expect(find.text('skipped_positions'), findsWidgets);
        await _tap(tester, '操作');

        // M18: explicit units [2,1], exception 11 then success; selection is local.
        before = await _metrics();
        await _tap(tester, '单元探测');
        await _enter(tester, '单元 ID 列表（逗号分隔）', '2,1');
        await _tap(tester, '预览探测范围');
        await _idle(tester, engine);
        _delta(
          before,
          await _metrics(),
          reads: 1,
          writes: 0,
          reason: 'one valid unit and one exception-only probe',
        );
        await _reveal(tester, find.text('协议异常响应'));
        expect(find.text('有响应'), findsOneWidget);
        final successful = find
            .ancestor(of: find.text('单元 1'), matching: find.byType(Card))
            .first;
        final select = find.descendant(
          of: successful,
          matching: find.text('仅选择此连接与单元到草稿'),
        );
        before = await _metrics();
        await _tapFinder(tester, select);
        _same(before, await _metrics(), 'probe result selection');

        // M21: absent app-private parent guarantees log-open failure before I/O.
        final impossible =
            'missing-audit-${DateTime.now().microsecondsSinceEpoch}/attempt.jsonl';
        await _tap(tester, '持久写入审计设置');
        await _enter(tester, '应用私有审计文件（留空禁用）', impossible);
        await _tap(tester, '临时应用');
        before = await _metrics();
        await _typedWrite(tester, '29');
        await _tap(tester, '确认执行');
        await _idle(tester, engine);
        _same(before, await _metrics(), 'write audit open failure');
        await _tap(tester, '会话');
        await _reveal(tester, find.textContaining('设备操作未执行'));
        expect(find.text('failed'), findsWidgets);
        await _tap(tester, '操作');
        await _tap(tester, '持久写入审计设置');
        await _enter(tester, '应用私有审计文件（留空禁用）', '');
        await _tap(tester, '临时应用');

        // M22: start read-only service through UI; health itself does not read.
        before = await _metrics();
        await _controller(tester, write: false);
        await _tap(tester, '明确启动');
        await _health(tester, readOnly: true);
        _same(
          before,
          await _metrics(),
          'read-only controller startup and health',
        );
        var response = await _api('/write', {
          'type': 'holding',
          'address': 0,
          'values': [31],
        });
        expect(response.status, 403);
        _same(before, await _metrics(), 'read-only HTTP write rejection');
        response = await _api('/read', {
          'type': 'holding',
          'address': 0,
          'count': 2,
        });
        expect(response.status, 200);
        expect(mbMap(response.body)['values'], [7, 42]);
        _delta(
          before,
          await _metrics(),
          reads: 1,
          writes: 0,
          reason: 'explicit local controller read',
        );
        await _tap(tester, '停止当前操作');
        await _idle(tester, engine);
        await _closed(tester);

        // Cancellation at the write-scope review never opens the local listener.
        before = await _metrics();
        await _controller(tester, write: true);
        await _tap(tester, '取消');
        await _tap(tester, '取消');
        await _closed(tester);
        _same(
          before,
          await _metrics(),
          'cancelled controller write-scope review',
        );
        await _controller(tester, write: true);
        await _tap(tester, '明确启动');
        await _health(tester, readOnly: false);
        response = await _api('/write', {
          'type': 'holding',
          'address': 1,
          'values': [99],
        });
        expect(response.status, 403);
        response = await _api('/write', {
          'type': 'holding',
          'address': 0,
          'values': [31],
          'unit_id': 2,
        });
        expect(response.status, 403);
        response = await _api('/write', {
          'type': 'holding',
          'address': 0,
          'values': [31],
        }, origin: 'http://127.0.0.1');
        expect(response.status, 403);
        _same(before, await _metrics(), 'scope/unit/browser-origin rejections');
        response = await _api('/write', {
          'type': 'holding',
          'address': 0,
          'values': [31],
        });
        expect(response.status, 204);
        _delta(
          before,
          await _metrics(),
          reads: 0,
          writes: 1,
          reason: 'explicit authorized local write',
        );
        response = await _api('/read', {
          'type': 'holding',
          'address': 0,
          'count': 1,
        });
        expect(response.status, 200);
        expect(mbMap(response.body)['values'], [31]);
        response = await _api('/write', {
          'type': 'holding',
          'address': 0,
          'values': [7],
        });
        expect(response.status, 204);
        await _tap(tester, '停止当前操作');
        await _idle(tester, engine);
        await _closed(tester);
        await _tap(tester, '会话');
        await _tap(tester, '刷新会话统计');
        expect(find.text('本次应用会话累计'), findsOneWidget);
        await _tap(tester, '关闭');
        expect(tester.takeException(), isNull);
        if (Platform.isAndroid) await binding.convertFlutterSurfaceToImage();
        await tester.pump();
        await binding.takeScreenshot(
          'flutter-modbus-04-advanced-fixture-complete',
        );
      } finally {
        // Only the disposable fixture is restored. No external endpoint is used.
        final state = mbMap(await engine.command({'op': 'state'}));
        if (state['running'] == true) {
          await engine.command({'op': 'cancel', 'run_id': state['run_id']});
          await _idle(tester, engine);
        }
        await tester.pumpWidget(const SizedBox());
        await tester.pump(const Duration(milliseconds: 100));
        await engine.open();
        final preview = mbMap(
          await engine.command({
            'op': 'preview',
            'request': {
              'id': 'advanced-fixture-restore',
              'protocol': 'modbus',
              'action': 'write-registers',
              'endpoint': 'tcp://127.0.0.1:48415',
              'timeout': '5s',
              'params': {
                'unit': 1,
                'address': 0,
                'count': 2,
                'values': [7, 42],
              },
            },
          }),
        );
        await engine.command({
          'op': 'run',
          'token': preview['token'],
          'confirmed': true,
        });
        await _idle(tester, engine);
        await engine.command({
          'op': 'config.save',
          'source': original['source'],
        });
        await engine.close();
        await platform.invoke('settings.save', {
          for (final key in ['readOnly', 'history', 'collection'])
            if (settings.containsKey(key)) key: settings[key],
        });
      }
    },
    timeout: const Timeout(Duration(minutes: 7)),
  );
}

void main() {
  WidgetController.hitTestWarningShouldBeFatal = true;
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  registerAdvancedModbusIntegrationTests();
}

Future<void> _fc23(WidgetTester t) async {
  await _tap(t, 'FC23 读写事务');
  await _enter(t, '写入起始地址', '0');
  await _enter(t, '寄存器字（逗号分隔 0–65535）', '23');
  await _enter(t, 'FC23 读取起始地址', '0');
  await _enter(t, 'FC23 读取数量', '2');
  await _tap(t, '预览编码与目标');
  await _wait(t, () => find.text('确认执行写操作').evaluate().isNotEmpty);
}

Future<void> _typedWrite(WidgetTester t, String value) async {
  await _tap(t, '写入类型数值 / 寄存器');
  await _enter(t, '写入起始地址', '0');
  await _enter(t, '精确数值', value);
  await _tap(t, '预览编码与目标');
  await _wait(t, () => find.text('确认执行写操作').evaluate().isNotEmpty);
}

Future<void> _raw(
  WidgetTester t, {
  required bool write,
  required String hex,
  required String count,
}) async {
  await _tap(t, '原始 PDU');
  if (write) {
    await _tapFinder(t, find.byKey(const ValueKey('操作分类')));
    await _tap(t, 'write-raw');
  }
  await _enter(t, 'PDU 十六进制（首字节为功能码）', hex);
  await _enter(t, '明确操作起始地址', '0');
  await _enter(t, '明确操作数量', count);
  await _tap(t, '解析并预览');
  if (write) await _wait(t, () => find.text('确认执行写操作').evaluate().isNotEmpty);
}

Future<void> _controller(WidgetTester t, {required bool write}) async {
  await _tap(t, '本机 HTTP 服务');
  await _enter(t, '数字回环监听地址', '127.0.0.1:18123');
  if (write) {
    await _tapFinder(t, find.widgetWithText(SwitchListTile, '开放有限写入范围'));
    await _enter(t, '可写单元', '1');
    await _enter(t, '可写起始地址', '0');
    await _enter(t, '可写数量', '1');
  }
  await _tap(t, '检查服务范围');
  await _wait(t, () => find.text('明确启动').evaluate().isNotEmpty);
}

class _Reply {
  _Reply(this.status, this.body);
  final int status;
  final Object? body;
}

Future<_Reply> _api(
  String path,
  Map<String, dynamic>? body, {
  String? origin,
}) async {
  final client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
  try {
    final request = body == null
        ? await client.getUrl(Uri.parse('http://127.0.0.1:18123$path'))
        : await client.postUrl(Uri.parse('http://127.0.0.1:18123$path'));
    if (origin != null) request.headers.set('Origin', origin);
    if (body != null) {
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(body));
    }
    final response = await request.close().timeout(const Duration(seconds: 3));
    final text = await utf8.decoder.bind(response).join();
    Object? decoded = text;
    try {
      decoded = jsonDecode(text);
    } catch (_) {}
    return _Reply(response.statusCode, decoded);
  } finally {
    client.close(force: true);
  }
}

Future<void> _health(WidgetTester t, {required bool readOnly}) async {
  final end = DateTime.now().add(const Duration(seconds: 20));
  while (true) {
    try {
      final response = await _api('/health', null);
      if (response.status == 200) {
        expect(mbMap(response.body)['read_only'], readOnly);
        return;
      }
    } on SocketException {
      /* Wait only for this explicitly started listener. */
    }
    if (DateTime.now().isAfter(end))
      throw TestFailure('Explicit local controller did not start');
    await t.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _closed(WidgetTester t) async {
  final end = DateTime.now().add(const Duration(seconds: 10));
  while (true) {
    try {
      await _api('/health', null);
    } on SocketException {
      return;
    }
    if (DateTime.now().isAfter(end))
      throw TestFailure('Cancelled local controller still accepts connections');
    await t.pump(const Duration(milliseconds: 100));
  }
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

void _delta(
  Map<String, dynamic> a,
  Map<String, dynamic> b, {
  required int reads,
  required int writes,
  required String reason,
}) {
  expect(
    b['modbus_reads'],
    (a['modbus_reads'] as int) + reads,
    reason: '$reason reads',
  );
  expect(
    b['modbus_writes'],
    (a['modbus_writes'] as int) + writes,
    reason: '$reason writes',
  );
}

void _same(Map<String, dynamic> a, Map<String, dynamic> b, String reason) =>
    _delta(a, b, reads: 0, writes: 0, reason: reason);
Future<void> _wait(WidgetTester t, bool Function() ready) async {
  final end = DateTime.now().add(const Duration(seconds: 30));
  while (!ready()) {
    if (DateTime.now().isAfter(end))
      throw TestFailure('Timed out waiting for advanced Modbus UI');
    await t.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _tap(WidgetTester t, String label) =>
    _tapFinder(t, find.text(label).last);
Future<void> _tapFinder(WidgetTester t, Finder finder) async {
  await _reveal(t, finder);
  await Scrollable.ensureVisible(t.element(finder), alignment: 0.5);
  await waitForModbusInteraction(t, finder);
  await t.tap(finder);
  await t.pump(const Duration(milliseconds: 400));
}

Future<void> _enter(WidgetTester t, String label, String value) async {
  final finder = find.byKey(ValueKey(label));
  await _wait(t, () => finder.evaluate().isNotEmpty);
  await t.ensureVisible(finder);
  await t.pump();
  await t.enterText(finder, value);
  await t.testTextInput.receiveAction(TextInputAction.done);
  await t.pump();
}

Future<void> _idle(WidgetTester t, Engine engine) async {
  final end = DateTime.now().add(const Duration(seconds: 30));
  while (true) {
    await t.pump(const Duration(milliseconds: 150));
    final state = mbMap(await engine.command({'op': 'state'}));
    if (state['running'] != true) {
      await t.pump(const Duration(milliseconds: 500));
      return;
    }
    if (DateTime.now().isAfter(end))
      throw TestFailure('Advanced Modbus operation did not stop');
  }
}

Future<void> _reveal(WidgetTester t, Finder finder) async {
  if (finder.evaluate().isNotEmpty) return;
  final scrollable = find
      .byWidgetPredicate(
        (widget) =>
            widget is Scrollable && widget.axisDirection == AxisDirection.down,
      )
      .last;
  if (scrollable.evaluate().isEmpty) {
    await _wait(t, () => finder.evaluate().isNotEmpty);
    return;
  }
  await t.drag(scrollable, const Offset(0, 3000));
  await t.pump(const Duration(milliseconds: 300));
  for (var step = 0; step < 28 && finder.evaluate().isEmpty; step++) {
    await t.drag(scrollable, const Offset(0, -320));
    await t.pump(const Duration(milliseconds: 120));
  }
  await _wait(t, () => finder.evaluate().isNotEmpty);
}
