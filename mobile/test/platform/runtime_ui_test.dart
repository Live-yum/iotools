import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/shared/review_dialog.dart';
import 'package:iotools_mobile/features/modbus/modbus_workspace.dart';
import 'package:iotools_mobile/features/opcua/opcua_workspace.dart';
import 'package:iotools_mobile/features/opcua/opcua_typed_editor.dart';
import '../support/fake_engine.dart';
import '../modbus/modbus_workspace_test.dart' show FakeModbusHost;
import '../features/opcua/opcua_workspace_test.dart' show FakeUa;

class RuntimePlatform extends FakePlatform {
  RuntimePlatform(this.name, this.capabilities);
  final String name;
  final JsonMap capabilities;
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    if (method == 'settings.get')
      return {
        'platform': name,
        'theme': 'dark',
        'collection': 'iotools.yaml',
        'capabilities': capabilities,
      };
    return super.invoke(method, args);
  }
}

Future<void> touch(WidgetTester tester, Finder target) async {
  await tester.ensureVisible(target);
  await tester.pumpAndSettle();
  await tester.tap(target);
  await tester.pumpAndSettle();
}

void life(WidgetTester tester, AppLifecycleState state) {
  if (state == AppLifecycleState.resumed) {
    if (tester.binding.lifecycleState == AppLifecycleState.paused) tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    if (tester.binding.lifecycleState == AppLifecycleState.hidden) tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
  }
  tester.binding.handleAppLifecycleStateChanged(state);
}
void main() {
  testWidgets(
    'HTTP write review distinguishes exact JSON number and string; hidden cancels once',
    (t) async {
      bool? result;
      final request = {
        'id': 'n',
        'protocol': 'http',
        'action': 'POST',
        'endpoint': 'http://localhost',
        'params': {
          'json': {
            'number': const ExactNumber('18446744073709551615'),
            'string': '18446744073709551615',
          },
        },
      };
      await t.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: TextButton(
                onPressed: () async => result = await reviewExecution(context, {
                  'request': request,
                }),
                child: const Text('预览'),
              ),
            ),
          ),
        ),
      );
      await touch(t, find.text('预览'));
      final body = t
          .widget<SelectableText>(find.byKey(const ValueKey('review_json')))
          .data!;
      expect(body, contains('"number":18446744073709551615'));
      expect(body, contains('"string":"18446744073709551615"'));
      life(t, AppLifecycleState.inactive);
      await t.pump();
      expect(find.text('确认执行写操作'), findsOneWidget);
      expect(result, isNull);
      life(t, AppLifecycleState.hidden);
      life(t, AppLifecycleState.paused);
      life(t, AppLifecycleState.resumed);
      await t.pumpAndSettle();
      expect(result, false);
      expect(find.text('预览'), findsOneWidget);
      expect(find.text('确认执行写操作'), findsNothing);
      life(t, AppLifecycleState.resumed);
      await t.pumpWidget(const SizedBox());
    },
  );
  for (final platform in ['ios', 'web', 'windows']) {
    testWidgets('$platform settings expose real device capability only', (
      t,
    ) async {
      final engine = FakeEngine(),
          services = RuntimePlatform(platform, {
            'usb': false,
            'serial': platform == 'windows',
          });
      await t.pumpWidget(IotoolsApp(engine: engine, platform: services));
      await t.pumpAndSettle();
      await touch(t, find.byKey(const ValueKey('nav_settings')));
      if (platform == 'windows') {
        await t.scrollUntilVisible(find.byKey(const ValueKey('open_serial_draft')), 350, scrollable: find.byType(Scrollable).last);
        await t.pumpAndSettle();
      }
      expect(find.text('USB 串口'), findsNothing);
      expect(
        find.byKey(const ValueKey('open_serial_draft')),
        platform == 'windows' ? findsOneWidget : findsNothing,
      );
      expect(engine.calls.where((c) => c['op'] == 'run'), isEmpty);
      expect(services.calls.where((c) => c.startsWith('usb.')), isEmpty);
      await t.pumpWidget(const SizedBox());
      await t.pump();
    });
  }
  testWidgets(
    'shell hidden plus paused cancels once, inactive keeps drafts and session live',
    (t) async {
      final engine = FakeEngine();
      await t.pumpWidget(
        IotoolsApp(
          engine: engine,
          platform: RuntimePlatform('linux', {'usb': false, 'serial': true}),
        ),
      );
      await t.pumpAndSettle();
      final session = t
          .widget<WorkspaceShell>(find.byType(WorkspaceShell))
          .session;
      session.source = 'only memory';
      session.started({'run_id': 'run'});
      life(t, AppLifecycleState.inactive);
      await t.pump();
      expect(engine.pauseCount, 0);
      expect(session.paused, false);
      life(t, AppLifecycleState.hidden);
      life(t, AppLifecycleState.paused);
      await t.pumpAndSettle();
      expect(engine.pauseCount, 1);
      expect(session.paused, true);
      expect(session.source, 'only memory');
      life(t, AppLifecycleState.resumed);
      await t.pumpAndSettle();
      expect(session.paused, false);
      expect(engine.calls.where((c) => c['op'] == 'run'), isEmpty);
      await t.pumpWidget(const SizedBox());
      await t.pump();
    },
  );
  testWidgets(
    'desktop Modbus RTU controls only prepare precise serial configuration',
    (t) async {
      final host = FakeModbusHost()
        ..state = {
          'capabilities': {'serial': true, 'usb': false},
          'platform': 'windows',
        };
      await t.pumpWidget(MaterialApp(home: ModbusWorkspace(host: host)));
      await t.pumpAndSettle();
      await touch(t, find.text('操作'));
      await touch(t, find.byKey(const ValueKey('连接参数')));
      await touch(t, find.byKey(const ValueKey('传输')));
      expect(find.text('usb'), findsNothing);
      await touch(t, find.text('rtu').last);
      await t.enterText(
        find.byKey(const ValueKey('端点 URI（网络地址或串口路径）')),
        'rtu://COM7',
      );
      await t.enterText(find.byKey(const ValueKey('波特率')), '19200');
      await touch(t, find.text('临时应用'));
      await touch(t, find.text('寄存器'));
      await touch(t, find.byKey(const ValueKey('应用临时设置到请求草稿')));
      expect(host.request['endpoint'], 'rtu://COM7');
      expect(mapOf(host.request['params'])['baud'], 19200);
      expect(mapOf(host.request['params'])['data_bits'], 8);
      expect(host.runs, isEmpty);
      expect(host.saved, isEmpty);
      expect(host.commands, isEmpty);
      await t.pumpWidget(const SizedBox());
      host.dispose();
    },
  );
  testWidgets(
    'OPC write review expires on hidden without paused; duplicate hidden is idle',
    (t) async {
      final fake = FakeUa();
      await t.pumpWidget(
        MaterialApp(
          home: OpcuaWorkspace(
            request: fake.request,
            command: fake.command,
            events: fake.events.stream,
            readOnly: false,
            onPrepare: (_) {},
          ),
        ),
      );
      await t.pumpAndSettle();
      await touch(t, find.byKey(const ValueKey('ua-tab-attributes')));
      await touch(t, find.byKey(const ValueKey('ua-refresh')));
      await touch(t, find.byKey(const ValueKey('ua-write')));
      await t.enterText(
        find.byKey(const ValueKey('ua-write-value-Int32')),
        '99',
      );
      await touch(t, find.byKey(const ValueKey('ua-preview-write')));
      life(t, AppLifecycleState.inactive);
      await t.pump();
      expect(find.byKey(const ValueKey('ua-confirm-run')), findsOneWidget);
      life(t, AppLifecycleState.hidden);
      life(t, AppLifecycleState.hidden);
      await t.pump();
      expect(
        fake.commands.where((c) => c['op'] == 'subscriptions.stop-all'),
        hasLength(1),
      );
      expect(fake.runs('write'), 0);
      life(t, AppLifecycleState.resumed);
      await t.pumpAndSettle();
      expect(find.byKey(const ValueKey('ua-confirm-run')), findsNothing);
      expect(fake.runs('write'), 0);
      await t.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
  testWidgets(
    'OPC nested typed array draft closes on hidden without changing its value',
    (t) async {
      Object? change;
      await t.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: UaTypedEditor(
              initialType: 'Byte[]',
              initialValue: const ['7'],
              onChanged: (value) => change = value,
            ),
          ),
        ),
      );
      await t.pumpAndSettle();
      await touch(t, find.text('添加元素'));
      await t.enterText(
        find.byKey(const ValueKey('ua-array-element-Byte')),
        '99',
      );
      life(t, AppLifecycleState.inactive);
      await t.pump();
      expect(find.text('应用元素'), findsOneWidget);
      life(t, AppLifecycleState.hidden);
      life(t, AppLifecycleState.resumed);
      await t.pumpAndSettle();
      expect(find.text('应用元素'), findsNothing);
      expect(change, isNull);
      life(t, AppLifecycleState.resumed);
      await t.pumpWidget(const SizedBox());
    },
  );
}
