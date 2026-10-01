import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/opcua/opcua_workspace.dart';
import 'package:iotools_mobile/features/opcua/opcua_typed_editor.dart';

class FakeUa {
  final events = StreamController<UaMap>.broadcast(sync: true);
  final commands = <UaMap>[];
  final previews = <String, UaMap>{};
  final request = <String, dynamic>{
    'id': 'opc',
    'name': 'OPC',
    'protocol': 'opcua',
    'action': 'browse',
    'endpoint': 'opc.tcp://127.0.0.1:48410',
    'params': {
      'node_id': 'ns=1;i=85',
      'security_policy': 'None',
      'security_mode': 'None',
      'allow_insecure': true,
    },
  };
  final subscriptions = <UaMap>[];
  int sequence = 0;
  Future<dynamic> command(UaMap c) async {
    commands.add(c);
    switch (c['op']) {
      case 'preview':
        final token = 'p${previews.length}';
        final r = uaMap(c['request']);
        previews[token] = r;
        return {
          'token': token,
          'request': r,
          'mutates': ['write', 'call'].contains(r['action']),
        };
      case 'run':
        final r = previews[c['token']]!, id = 'r${commands.length}';
        if (r['action'] == 'subscribe') {
          subscriptions.add({
            'id': id,
            'status': 'running',
            'endpoint': r['endpoint'],
            'node_ids': [uaMap(r['params'])['node_id']],
            'last_event': {'value': 42, 'status': 'Good'},
          });
          return {'run_id': id, 'subscription_id': id, 'background': true};
        }
        Future<void>.delayed(Duration.zero, () {
          void send(String kind, UaMap data) => events.add({
            'run_id': id,
            'seq': ++sequence,
            'kind': kind,
            'data': data,
          });
          if (r['action'] == 'browse')
            send('reference', {
              'node_id': 'ns=1;s=Temperature',
              'display_name': 'Temperature',
              'node_class': 'Variable',
            });
          if (r['action'] == 'browse')
            send('reference', {
              'node_id': 'ns=1;s=Double',
              'display_name': 'Double',
              'node_class': 'Method',
            });
          if (r['action'] == 'method-arguments')
            send('method-arguments', {
              'inputs': [
                {'name': '输入值', 'type': 'Int32', 'description': '精确参数'},
              ],
              'outputs': [
                {'name': '输出值', 'type': 'Int32'},
              ],
            });
          if (r['action'] == 'call')
            send('method', {
              'status': 'Good',
              'outputs': [12],
            });
          if (r['action'] == 'attributes')
            send('attribute', {
              'node_id': uaMap(r['params'])['node_id'],
              'attribute': 'Value',
              'value_type_name': 'Int32',
              'value': 7,
              'status': 'Good',
            });
          if (r['action'] == 'discover')
            send('endpoint', {
              'url': 'opc.tcp://127.0.0.1:48410',
              'security_policy': 'None',
              'security_mode': 'None',
              'certificate_sha256': 'UNTRUSTED',
              'identity_tokens': ['Anonymous'],
            });
          send('done', {'status': 'completed'});
        });
        return {'run_id': id};
      case 'subscriptions.list':
        return subscriptions;
      case 'subscriptions.stop':
        for (final sub in subscriptions) {
          if (sub['id'] == c['subscription_id']) sub['status'] = 'cancelled';
        }
        return {};
      case 'subscriptions.stop-all':
        for (final sub in subscriptions) {
          sub['status'] = 'cancelled';
        }
        return {};
      case 'opcua.connections':
        return [
          {
            'endpoint': 'opc.tcp://127.0.0.1:48410',
            'node_id': 'ns=1;i=85',
            'security_policy': 'None',
            'security_mode': 'None',
            'last_connected': '2026-10-01T06:00:00Z',
          },
        ];
      default:
        return {};
    }
  }

  int runs(String action) => commands
      .where(
        (c) => c['op'] == 'run' && previews[c['token']]?['action'] == action,
      )
      .length;
}

Future<void> tap(WidgetTester tester, String key) async {
  final f = find.byKey(ValueKey(key));
  await tester.ensureVisible(f);
  await tester.pump();
  await tester.tap(f);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'cached navigation is local; typed cancel and invalid value do not run',
    (tester) async {
      final fake = FakeUa();
      await tester.pumpWidget(
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
      expect(fake.commands, isEmpty);
      await tap(tester, 'ua-refresh');
      expect(fake.runs('browse'), 1);
      expect(find.text('Temperature'), findsOneWidget);
      final before = fake.commands.length;
      await tap(tester, 'ua-node-ns=1;s=Temperature');
      await tap(tester, 'ua-back');
      await tap(tester, 'ua-forward');
      expect(fake.commands.length, before);
      await tap(tester, 'ua-tab-attributes');
      await tap(tester, 'ua-refresh');
      await tap(tester, 'ua-write');
      await tester.enterText(
        find.byKey(const ValueKey('ua-write-value-Int32')),
        '99',
      );
      await tap(tester, 'ua-preview-write');
      expect(find.text('确认 OPC UA 写入 / 调用'), findsOneWidget);
      await tap(tester, 'ua-review-cancel');
      expect(fake.runs('write'), 0);
      await tester.enterText(
        find.byKey(const ValueKey('ua-write-value-Int32')),
        '2147483648',
      );
      await tap(tester, 'ua-preview-write');
      expect(find.textContaining('Int32 范围'), findsOneWidget);
      expect(fake.runs('write'), 0);
      await tester.enterText(
        find.byKey(const ValueKey('ua-write-value-Int32')),
        '99',
      );
      await tap(tester, 'ua-preview-write');
      await tap(tester, 'ua-confirm-run');
      expect(fake.runs('write'), 1);
      await tester.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
  testWidgets(
    'discovery selection never auto-trusts advertised fingerprint or connects',
    (tester) async {
      final fake = FakeUa();
      UaMap? prepared;
      await tester.pumpWidget(
        MaterialApp(
          home: OpcuaWorkspace(
            request: fake.request,
            command: fake.command,
            events: fake.events.stream,
            readOnly: false,
            onPrepare: (r) => prepared = r,
          ),
        ),
      );
      await tap(tester, 'ua-tab-discovery');
      await tap(tester, 'ua-refresh');
      final before = fake.commands.length;
      await tap(tester, 'ua-select-endpoint');
      await tap(tester, 'ua-apply-connection');
      expect(find.textContaining('必须明确勾选 None'), findsOneWidget);
      expect(prepared, isNull);
      final toggle = find.widgetWithText(
        SwitchListTile,
        '明确允许 None 无加密（仅可信测试环境）',
      );
      await tester.ensureVisible(toggle);
      await tester.tap(toggle);
      await tester.pumpAndSettle();
      await tap(tester, 'ua-apply-connection');
      expect(prepared, isNotNull);
      expect(uaMap(prepared!['params'])['server_cert_sha256'], isNull);
      expect(fake.commands.length, before);
      await tester.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
  testWidgets('structured and array fields edit values without JSON textarea', (
    tester,
  ) async {
    UaMap value = {};
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: UaTypedEditor(
              initialType: 'LocalizedText',
              initialValue: const {'text': '', 'locale': ''},
              onChanged: (v) => value = v,
            ),
          ),
        ),
      ),
    );
    await tester.enterText(
      find.byKey(const ValueKey('ua-value-LocalizedText-text')),
      '中文🙂',
    );
    await tester.enterText(
      find.byKey(const ValueKey('ua-value-LocalizedText-locale')),
      'zh-CN',
    );
    expect(uaValidate('LocalizedText', value['value']), {
      'text': '中文🙂',
      'locale': 'zh-CN',
    });
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: UaTypedEditor(
            key: const ValueKey('array'),
            initialType: 'Byte[]',
            initialValue: const ['255'],
            onChanged: (v) => value = v,
          ),
        ),
      ),
    );
    await tester.tap(find.text('添加元素'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('ua-array-element-Byte')),
      '0',
    );
    await tester.tap(find.text('应用元素'));
    await tester.pumpAndSettle();
    expect(value['value'], ['255', '0']);
  });
  testWidgets('background removes write review and cannot replay it', (
    tester,
  ) async {
    final fake = FakeUa();
    await tester.pumpWidget(
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
    await tap(tester, 'ua-tab-attributes');
    await tap(tester, 'ua-refresh');
    await tap(tester, 'ua-write');
    await tester.enterText(
      find.byKey(const ValueKey('ua-write-value-Int32')),
      '99',
    );
    await tap(tester, 'ua-preview-write');
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pumpAndSettle();
    expect(
      fake.commands.where((c) => c['op'] == 'subscriptions.stop-all').length,
      1,
    );
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('ua-confirm-run')), findsNothing);
    expect(fake.runs('write'), 0);
    await tester.pumpWidget(const SizedBox());
    await fake.events.close();
  });
  testWidgets(
    'method signature populates ordered typed form without invocation',
    (tester) async {
      final fake = FakeUa();
      await tester.pumpWidget(
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
      await tap(tester, 'ua-refresh');
      await tap(tester, 'ua-method-ns=1;s=Double');
      await tap(tester, 'ua-fetch-signature');
      expect(fake.runs('call'), 0);
      await tap(tester, 'ua-open-method-call');
      await tester.enterText(
        find.byKey(const ValueKey('ua-argument-0-Int32')),
        '6',
      );
      await tap(tester, 'ua-preview-call');
      await tap(tester, 'ua-review-cancel');
      expect(fake.runs('call'), 0);
      await tap(tester, 'ua-preview-call');
      await tap(tester, 'ua-confirm-run');
      expect(fake.runs('call'), 1);
      expect(find.byKey(const ValueKey('ua-method-outputs')), findsOneWidget);
      expect(
        tester
            .widget<SelectableText>(
              find.byKey(const ValueKey('ua-method-outputs')),
            )
            .data,
        '[12]',
      );
      await tester.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
  testWidgets('independent subscriptions coexist and stop only exact ID', (
    tester,
  ) async {
    final fake = FakeUa();
    await tester.pumpWidget(
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
    await tap(tester, 'ua-tab-subscriptions');
    for (var i = 0; i < 2; i++) {
      await tap(tester, 'ua-add-subscription');
      await tap(tester, 'ua-preview-subscribe');
      await tap(tester, 'ua-confirm-run');
    }
    expect(fake.subscriptions.where((s) => s['status'] == 'running').length, 2);
    final target = '${fake.subscriptions.first['id']}';
    await tap(tester, 'ua-stop-$target');
    expect(fake.subscriptions.first['status'], 'cancelled');
    expect(fake.subscriptions.last['status'], 'running');
    await tap(tester, 'ua-tab-attributes');
    await tap(tester, 'ua-refresh');
    expect(fake.runs('attributes'), 1);
    expect(fake.subscriptions.last['status'], 'running');
    await tester.pumpWidget(const SizedBox());
    await fake.events.close();
  });
  testWidgets(
    'reference filters and history selection stay local until explicit read',
    (tester) async {
      final fake = FakeUa();
      await tester.pumpWidget(
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
      await tap(tester, 'ua-tab-references');
      await tap(tester, 'ua-reference-filter');
      await tester.enterText(
        find.byKey(const ValueKey('ua-field-引用类型 NodeId（可留空）')),
        'i=46',
      );
      await tap(tester, 'ua-form-submit');
      expect(fake.commands, isEmpty);
      await tap(tester, 'ua-refresh');
      expect(
        uaMap(fake.previews.values.last['params'])['reference_type'],
        'i=46',
      );
      await tap(tester, 'ua-tab-discovery');
      await tap(tester, 'ua-connections');
      expect(fake.commands.last['op'], 'opcua.connections');
      final before = fake.commands.length;
      await tester.tap(find.text('选择并检查连接草稿'));
      await tester.pumpAndSettle();
      expect(find.text('连接与安全草稿'), findsOneWidget);
      expect(fake.commands.length, before);
      await tester.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
  testWidgets('cancelled identity review never creates files', (tester) async {
    final fake = FakeUa();
    await tester.pumpWidget(
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
    await tap(tester, 'ua-tab-discovery');
    await tap(tester, 'ua-identity');
    await tap(tester, 'ua-preview-identity');
    expect(find.text('生成新的证书与私钥'), findsOneWidget);
    await tap(tester, 'ua-review-cancel');
    expect(fake.commands.where((c) => c['op'] == 'opcua.identity'), isEmpty);
    await tester.pumpWidget(const SizedBox());
    await fake.events.close();
  });
  testWidgets(
    'large text and short landscape remain scrollable without overflow',
    (tester) async {
      final fake = FakeUa();
      tester.view.physicalSize = const Size(780, 800);
      tester.view.devicePixelRatio = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(2)),
            child: child!,
          ),
          home: OpcuaWorkspace(
            request: fake.request,
            command: fake.command,
            events: fake.events.stream,
            readOnly: false,
            onPrepare: (_) {},
          ),
        ),
      );
      expect(tester.takeException(), isNull);
      await tap(tester, 'ua-refresh');
      expect(tester.takeException(), isNull);
      await tap(tester, 'ua-tab-attributes');
      await tap(tester, 'ua-refresh');
      await tap(tester, 'ua-write');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await fake.events.close();
    },
  );
}
