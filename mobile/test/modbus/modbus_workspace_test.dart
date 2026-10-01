import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/modbus/modbus_controller.dart';
import 'package:iotools_mobile/features/modbus/modbus_models.dart';
import 'package:iotools_mobile/features/modbus/modbus_workspace.dart';
import 'package:iotools_mobile/shared/review_dialog.dart';
import '../../integration_test/modbus_interaction.dart';

class FakeModbusHost extends ChangeNotifier implements ModbusHost {
  @override
  Map<String, dynamic> request = {
    'id': 'meter',
    'name': '测试电表',
    'protocol': 'modbus',
    'action': 'read-holding',
    'endpoint': 'tcp://actual:502',
    'params': {'unit': 1, 'address': 0, 'count': 4},
  };
  @override
  Map<String, dynamic> state = {};
  @override
  List<Map<String, dynamic>> events = [];
  @override
  bool readOnly = false;
  @override
  String resultRunId = '';
  @override
  Map<String, dynamic>? resultRequest;
  @override
  Map<String, dynamic>? originalResultRequest;
  final stream = StreamController<Map<String, dynamic>>.broadcast();
  @override
  Stream<Map<String, dynamic>> get eventStream => stream.stream;
  final commands = <Map<String, dynamic>>[],
      runs = <Map<String, dynamic>>[],
      saved = <Map<String, dynamic>>[];
  final exports = <String, String>{};
  Completer<void>? heldRun;
  Future<bool> Function(Map<String, dynamic>)? review;
  @override
  Map<String, dynamic>? originalRequest(String runId) =>
      runId == resultRunId ? originalResultRequest : null;
  @override
  Future<dynamic> command(Map<String, dynamic> command) async {
    commands.add(mbClone(command));
    switch (command['op']) {
      case 'modbus.rules':
        return [];
      case 'modbus.interpret':
        return [
          for (final key in mbMap(command['words']).keys)
            {
              'address': int.parse(key),
              'u16': command['words'][key],
              'custom': '合法结果',
              'custom_numeric': '42',
            },
        ];
      case 'modbus.snapshot.save':
        return {'path': command['path']};
      case 'files.list':
        return [];
      case 'modbus.import':
        return {
          'token': 'one-use',
          'requests': [
            {
              'id': 'imported-holding',
              'action': 'read-holding',
              'endpoint': 'tcp://test:502',
              'params': {'unit': 1, 'address': 0, 'count': 1},
            },
          ],
          'warnings': ['未启用自动读取'],
        };
      case 'modbus.import.apply':
        return {'requests': [], 'backup': 'original.backup.yaml'};
      case 'modbus.stats':
        return {
          'session': {'reads': 2, 'writes': 1, 'errors': 0},
        };
      default:
        return {};
    }
  }

  @override
  void prepare(Map<String, dynamic> value) {
    request = mbClone(value);
    notifyListeners();
  }

  @override
  Future<void> saveRequest(Map<String, dynamic> value) async {
    saved.add(mbClone(value));
    request = mbClone(value);
    notifyListeners();
  }

  @override
  Future<void> reviewAndRun(Map<String, dynamic> value) async {
    if (review != null && !await review!(value)) return;
    runs.add(mbClone(value));
    if (heldRun != null) await heldRun!.future;
  }

  @override
  Future<void> exportText(String name, String text) async {
    exports[name] = text;
  }

  @override
  Future<void> collectionSaved(Map<String, dynamic> value) async {
    state = mbClone(value);
    notifyListeners();
  }

  @override
  void started(Map<String, dynamic> response) {
    state['run_id'] = response['run_id'];
  }

  void sample({
    String endpoint = 'tcp://actual:502',
    int unit = 1,
    String value = '18446744073709551615',
  }) {
    resultRunId = 'run-a';
    originalResultRequest = mbClone(request);
    resultRequest = {
      ...mbClone(request),
      'endpoint': endpoint,
      'params': {...mbMap(request['params']), 'unit': unit},
    };
    events = [
      {
        'seq': 0,
        'kind': 'started',
        'run_id': 'run-a',
        'data': {
          'source': {
            'request_id': 'meter',
            'endpoint': endpoint,
            'unit': unit,
            'action': 'read-holding',
          },
        },
      },
      {
        'seq': 1,
        'kind': 'registers',
        'run_id': 'run-a',
        'time': '2026-10-01T00:00:00Z',
        'data': [
          {'address': 0, 'u16': 65535, 'u64': value},
        ],
      },
    ];
    notifyListeners();
  }

  @override
  void dispose() {
    stream.close();
    super.dispose();
  }
}

Future<void> mount(WidgetTester tester, FakeModbusHost host) async {
  await tester.pumpWidget(
    MaterialApp(
      theme: ThemeData.dark(useMaterial3: true),
      home: ModbusWorkspace(host: host),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> tapText(WidgetTester tester, String text) async {
  final finder = find.text(text).last;
  await Scrollable.ensureVisible(tester.element(finder), alignment: 0.5);
  await tester.pump();
  await tester.tap(finder);
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 400));
  await tester.pump();
}

void main() {
  testWidgets('lazy offscreen device identification action opens without IO', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(480, 752);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final host = FakeModbusHost();
    await mount(tester, host);
    await tapText(tester, '操作');
    final action = find.text('读取设备标识');
    expect(action, findsNothing, reason: '设备卡片尚未由 ListView 构建');
    await revealModbusFinder(tester, action);
    final target = action.last;
    await Scrollable.ensureVisible(tester.element(target), alignment: .5);
    await waitForModbusInteraction(tester, target);
    await tester.tap(target);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('读取代码（1–4）')), findsOneWidget);
    expect(host.runs, isEmpty);
    expect(host.commands, isEmpty);
    await tapText(tester, '取消');
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('reveal waits for a delayed target without a scrollable', (
    tester,
  ) async {
    final ready = ValueNotifier(false);
    await tester.pumpWidget(
      MaterialApp(
        home: ValueListenableBuilder<bool>(
          valueListenable: ready,
          builder: (_, value, child) =>
              Text(value ? '延迟出现的目标' : '正在加载'),
        ),
      ),
    );
    expect(find.byType(Scrollable), findsNothing);
    Timer(const Duration(milliseconds: 100), () => ready.value = true);
    await revealModbusFinder(tester, find.text('延迟出现的目标'));
    expect(find.text('延迟出现的目标'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    ready.dispose();
  });
  testWidgets('nested write review cancel can reopen both write forms', (
    tester,
  ) async {
    final host = FakeModbusHost();
    await mount(tester, host);
    host.review = (request) => reviewExecution(
      tester.element(find.byType(ModbusWorkspace)),
      {'request': request, 'confirmation_required': true},
    );
    await tapText(tester, '操作');
    for (final label in ['FC23 读写事务', '写入类型数值 / 寄存器']) {
      await tapText(tester, label);
      await tapText(tester, '预览编码与目标');
      expect(find.text('确认执行写操作'), findsOneWidget);
      await tester.tap(find.text('取消').last);
      await tester.pump(const Duration(milliseconds: 350));
      expect(
        tester.widget<OutlinedButton>(find.byKey(ValueKey(label))).onPressed,
        isNull,
        reason: '嵌套确认页退场时，原写入表单还在完成提交回调',
      );
      final action = find.text(label).last;
      await Scrollable.ensureVisible(tester.element(action), alignment: .5);
      await waitForModbusInteraction(tester, action);
      await tester.tap(action);
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('写入起始地址')), findsOneWidget);
      expect(host.runs, isEmpty);
      expect(host.commands, isEmpty);
      expect(host.saved, isEmpty);
      await tapText(tester, '取消');
    }
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('opening and local navigation never run a device request', (
    tester,
  ) async {
    final host = FakeModbusHost()..sample();
    await mount(tester, host);
    expect(host.commands, isEmpty);
    expect(host.runs, isEmpty);
    await tapText(tester, '地址导航');
    await tester.enterText(
      find.byKey(const ValueKey('地址 / 0x / 相对值 / 唯一标签')),
      '0x10',
    );
    await tapText(tester, '仅应用窗口');
    expect(host.runs, isEmpty);
    expect(host.saved, isEmpty);
    expect(
      host.commands.every((command) => command['op'] == 'modbus.interpret'),
      isTrue,
    );
    expect(host.request['params']['address'], 0);
    await tapText(tester, '应用临时设置到请求草稿');
    expect(host.request['params']['address'], 16);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('layout cancellation preserves original request and table', (
    tester,
  ) async {
    final host = FakeModbusHost()..sample();
    final original = mbClone(host.request);
    await mount(tester, host);
    await tapText(tester, '列与矩阵');
    await tapText(tester, '取消');
    expect(host.request, original);
    expect(host.commands, isEmpty);
    expect(host.saved, isEmpty);
    expect(find.text('u16'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('typed write cancel leaves no execution or saved draft', (
    tester,
  ) async {
    final host = FakeModbusHost();
    await mount(tester, host);
    await tapText(tester, '操作');
    await tapText(tester, '写入类型数值 / 寄存器');
    await tester.enterText(
      find.byKey(const ValueKey('精确数值')),
      '9007199254740993',
    );
    await tapText(tester, '取消');
    expect(host.runs, isEmpty);
    expect(host.commands, isEmpty);
    expect(host.saved, isEmpty);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('readonly visibly disables writes and configuration saves', (
    tester,
  ) async {
    final host = FakeModbusHost()..readOnly = true;
    await mount(tester, host);
    final save = tester.widget<OutlinedButton>(
      find.byKey(const ValueKey('保存工作区配置')),
    );
    expect(save.onPressed, isNull);
    await tapText(tester, '操作');
    final write = tester.widget<OutlinedButton>(
      find.byKey(const ValueKey('写入类型数值 / 寄存器')),
    );
    expect(write.onPressed, isNull);
    expect(host.runs, isEmpty);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets(
    'one explicit read uses reviewed draft and prevents duplicate tap',
    (tester) async {
      final host = FakeModbusHost()..heldRun = Completer<void>();
      await mount(tester, host);
      await tapText(tester, '操作');
      final button = find.byKey(const ValueKey('预览并读取当前窗口'));
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pump();
      expect(host.runs.length, 1);
      expect(tester.widget<OutlinedButton>(button).onPressed, isNull);
      host.heldRun!.complete();
      await tester.pumpAndSettle();
      expect(host.runs.single['action'], 'read-holding');
      await tester.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  testWidgets(
    'snapshot refuses mismatched edited target instead of relabeling',
    (tester) async {
      final host = FakeModbusHost()
        ..sample(endpoint: 'tcp://actual:502', unit: 7);
      host.request['endpoint'] = 'tcp://edited:502';
      host.originalResultRequest!['endpoint'] = 'tcp://original:502';
      await mount(tester, host);
      await tapText(tester, '离线工具');
      await tapText(tester, '保存当前缓存快照');
      expect(find.textContaining('没有来源完整'), findsOneWidget);
      expect(
        host.commands.where(
          (command) => command['op'] == 'modbus.snapshot.save',
        ),
        isEmpty,
      );
      await tester.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  testWidgets('uint64 typed control sends exact max to shared review', (
    tester,
  ) async {
    final host = FakeModbusHost();
    host.request['params']['count'] = 2;
    await mount(tester, host);
    await tapText(tester, '操作');
    await tapText(tester, '写入类型数值 / 寄存器');
    await tester.tap(find.byKey(const ValueKey('数值类型')));
    await tester.pumpAndSettle();
    await tapText(tester, 'u64');
    await tester.enterText(
      find.byKey(const ValueKey('精确数值')),
      '18446744073709551615',
    );
    await tapText(tester, '预览编码与目标');
    expect(host.runs.single['params']['value'], '18446744073709551615');
    expect(host.runs.single['params']['value_type'], 'u64');
    expect(host.runs.single['params'].containsKey('count'), isFalse);
    expect(host.request['params']['count'], 2);
    expect(host.runs.single['action'], 'write-typed');
    expect(host.saved, isEmpty);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('u16 write from a two-word read leaves width to the shared encoder', (
    tester,
  ) async {
    final host = FakeModbusHost();
    host.request['params']['count'] = 2;
    await mount(tester, host);
    await tapText(tester, '操作');
    await tapText(tester, '写入类型数值 / 寄存器');
    await tester.enterText(find.byKey(const ValueKey('精确数值')), '17');
    await tapText(tester, '预览编码与目标');
    final request = host.runs.single;
    expect(request['action'], 'write-typed');
    expect(request['params']['value_type'], 'u16');
    expect(request['params']['value'], '17');
    expect(request['params'].containsKey('count'), isFalse);
    expect(host.request['params']['count'], 2);
    expect(host.saved, isEmpty);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets('rule preview cancellation retains the previous request', (
    tester,
  ) async {
    final host = FakeModbusHost()..sample();
    final original = mbClone(host.request);
    await mount(tester, host);
    await tapText(tester, '规则');
    await tapText(tester, '校验并预览');
    expect(find.text('合法结果'), findsOneWidget);
    await tapText(tester, '取消');
    await tapText(tester, '取消');
    expect(host.request, original);
    expect(host.runs, isEmpty);
    expect(host.saved, isEmpty);
    expect(host.commands.map((c) => c['op']), [
      'modbus.rules',
      'modbus.interpret',
    ]);
    await tester.pumpWidget(const SizedBox());
    host.dispose();
  });
  testWidgets(
    'snapshot serializes actual resolved source with captured words',
    (tester) async {
      final host = FakeModbusHost()
        ..sample(endpoint: 'tcp://resolved:502', unit: 7);
      await mount(tester, host);
      await tapText(tester, '离线工具');
      await tapText(tester, '保存当前缓存快照');
      await tapText(tester, '保存新快照');
      final snapshot = host.commands.singleWhere(
        (c) => c['op'] == 'modbus.snapshot.save',
      )['snapshot'];
      expect(snapshot['endpoint'], 'tcp://resolved:502');
      expect(snapshot['unit'], 7);
      expect(snapshot['values'], {'0': 65535});
      await tapText(tester, '关闭');
      await tester.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  testWidgets(
    'MTUI preview needs explicit selection and applies reviewed token',
    (tester) async {
      final host = FakeModbusHost();
      await mount(tester, host);
      await tapText(tester, '离线工具');
      await tapText(tester, '导入完整 MTUI 配置');
      await tester.enterText(find.byKey(const ValueKey('MTUI 配置 JSON')), '{}');
      await tapText(tester, '生成转换预览');
      expect(host.commands.map((c) => c['op']), ['modbus.import']);
      expect(host.runs, isEmpty);
      await tapText(tester, 'imported-holding · read-holding');
      await tapText(tester, '备份并追加到本机集合');
      final applied = host.commands.singleWhere(
        (c) => c['op'] == 'modbus.import.apply',
      );
      expect(applied['token'], 'one-use');
      expect(applied['request_ids'], ['imported-holding']);
      expect(applied['confirmed'], isTrue);
      expect(find.text('original.backup.yaml'), findsOneWidget);
      expect(host.state['backup'], 'original.backup.yaml');
      await tapText(tester, '关闭');
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  testWidgets(
    'FC23 form derives count from written words independently of read range',
    (tester) async {
      final host = FakeModbusHost();
      await mount(tester, host);
      await tapText(tester, '操作');
      await tapText(tester, 'FC23 读写事务');
      await tester.enterText(
        find.byKey(const ValueKey('寄存器字（逗号分隔 0–65535）')),
        '7,42',
      );
      await tester.enterText(find.byKey(const ValueKey('FC23 读取数量')), '1');
      await tapText(tester, '预览编码与目标');
      expect(host.runs.single['params']['count'], 2);
      expect(host.runs.single['params']['values'], [7, 42]);
      expect(host.runs.single['params']['read_count'], 1);
      await tester.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  test('controller freeze retains the selected immutable response', () {
    final host = FakeModbusHost()..sample();
    final controller = ModbusController(host);
    controller.freeze(true);
    final frame = controller.current;
    host.events.add({
      'seq': 2,
      'kind': 'registers',
      'run_id': 'run-a',
      'time': '2026-10-01T01:00:00Z',
      'data': [
        {'address': 0, 'u16': 1},
      ],
    });
    host.notifyListeners();
    expect(controller.current, same(frame));
    controller.freeze(false);
    expect(controller.current!.words['0'], 1);
    controller.freeze(true);
    controller.edit({
      ...mbClone(controller.draft),
      'endpoint': 'tcp://another:502',
    });
    expect(controller.current, isNull);
    controller.dispose();
    host.dispose();
  });
}
