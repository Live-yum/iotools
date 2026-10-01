import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';

void registerKafkaManagementTests() {
  testWidgets(
    'Flutter Kafka Schema and Connect native management performs exact reviewed mutations',
    (t) async {
      const engine = MethodChannelEngine(), platform = MethodChannelPlatform();
      final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
      await platform.invoke('settings.save', {
        'theme': 'dark',
        'readOnly': false,
        'history': false,
        'collection': 'iotools.yaml',
      });
      await engine.close();
      await engine.open();
      final original = mapOf(await engine.command({'op': 'config.get'}));
      final before = await metrics();
      final list = <JsonMap>[
        {
          'id': 'schemas',
          'name': 'Schema 列表',
          'protocol': 'kafka',
          'action': 'schemas',
          'endpoint': 'http://127.0.0.1:48413',
        },
        {
          'id': 'schema_register',
          'name': '注册测试 Schema',
          'protocol': 'kafka',
          'action': 'register-schema',
          'endpoint': 'http://127.0.0.1:48413',
          'params': {'subject': 'fixture-schema'},
        },
        {
          'id': 'soft_delete',
          'name': '软删除测试 Schema',
          'protocol': 'kafka',
          'action': 'delete-subject',
          'endpoint': 'http://127.0.0.1:48413',
          'params': {'subject': 'fixture-schema'},
        },
        {
          'id': 'purge',
          'name': '永久清理测试 Schema',
          'protocol': 'kafka',
          'action': 'purge-subject',
          'endpoint': 'http://127.0.0.1:48413',
          'params': {'subject': 'fixture-schema'},
        },
        {
          'id': 'connectors',
          'name': 'Connector 列表',
          'protocol': 'kafka',
          'action': 'connectors',
          'endpoint': 'http://127.0.0.1:48413',
        },
        {
          'id': 'update',
          'name': '更新测试 Connector',
          'protocol': 'kafka',
          'action': 'update-connector',
          'endpoint': 'http://127.0.0.1:48413',
          'params': {
            'connector': 'local-demo',
            'json': {'connector.class': 'fixture', 'tasks.max': '1'},
          },
        },
        for (final a in ['pause', 'resume'])
          {
            'id': a,
            'name': '$a 测试 Connector',
            'protocol': 'kafka',
            'action': '$a-connector',
            'endpoint': 'http://127.0.0.1:48413',
            'params': {'connector': 'local-demo'},
          },
      ];
      await engine.command({
        'op': 'config.save',
        'source': jsonEncode({
          'version': 1,
          'profiles': {'local': {}},
          'requests': list,
        }),
      });
      await engine.close();
      try {
        await t.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await wait(t, () => find.text('注册测试 Schema').evaluate().isNotEmpty);
        await openRequest(t, '注册测试 Schema');
        await enter(t, 'schema_definition', '"string"');
        await run(t, engine, cancel: true);
        expect((await metrics())['http_mutations'], before['http_mutations']);
        await run(t, engine);
        expect(
          (await metrics())['schema_registrations'],
          (before['schema_registrations'] as int) + 1,
        );
        await back(t);
        await openRequest(t, 'Schema 列表');
        await run(t, engine, write: false);
        await tap(t, find.text('浏览实体 / 分区 / 管理操作'));
        await tap(t, find.text('fixture-schema'));
        expect(find.text('版本列表'), findsOneWidget);
        await binding.convertFlutterSurfaceToImage();
        await t.pump();
        await binding.takeScreenshot('flutter-kafka-03-schema-native-detail');
        await tap(t, find.text('关闭'));
        await back(t);
        await back(t);
        await openRequest(t, '软删除测试 Schema');
        await run(t, engine);
        expect(
          (await metrics())['schema_soft_deletes'],
          (before['schema_soft_deletes'] as int) + 1,
        );
        await back(t);
        await openRequest(t, '永久清理测试 Schema');
        await tap(t, find.text('连接、安全与高级参数'));
        await enter(t, 'param_confirm_subject', 'fixture-schema');
        await run(t, engine);
        expect(
          (await metrics())['schema_purges'],
          (before['schema_purges'] as int) + 1,
        );
        await back(t);
        await openRequest(t, '更新测试 Connector');
        await tap(t, find.byKey(const ValueKey('edit_json')));
        await tap(t, find.text('tasks.max'));
        await t.enterText(find.byKey(const ValueKey('input_dialog')), '2');
        await tap(t, find.text('应用').last);
        await tap(t, find.text('应用').last);
        await run(t, engine, cancel: true);
        expect(
          (await metrics())['connector_updates'],
          before['connector_updates'],
        );
        await run(t, engine);
        expect(
          (await metrics())['connector_updates'],
          (before['connector_updates'] as int) + 1,
        );
        await back(t);
        for (final a in ['pause', 'resume']) {
          await openRequest(t, '$a 测试 Connector');
          await run(t, engine);
          await back(t);
        }
        final after = await metrics();
        expect(
          after['connector_pauses'],
          (before['connector_pauses'] as int) + 1,
        );
        expect(
          after['connector_resumes'],
          (before['connector_resumes'] as int) + 1,
        );
        await openRequest(t, 'Connector 列表');
        await run(t, engine, write: false);
        await tap(t, find.text('浏览实体 / 分区 / 管理操作'));
        await tap(t, find.text('local-demo'));
        expect(find.text('准备更新配置'), findsOneWidget);
        await binding.takeScreenshot(
          'flutter-kafka-04-connector-native-actions',
        );
        expect(after['http_mutations'], (before['http_mutations'] as int) + 6);
      } finally {
        await t.pumpWidget(const SizedBox());
        await t.pump(const Duration(milliseconds: 300));
        await engine.open();
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

Future<JsonMap> metrics() async {
  final c = HttpClient();
  try {
    final r = await (await c.getUrl(
      Uri.parse('http://127.0.0.1:48413/metrics'),
    )).close();
    return mapOf(jsonDecode(await utf8.decoder.bind(r).join()));
  } finally {
    c.close(force: true);
  }
}

Future<void> wait(WidgetTester t, bool Function() ready) async {
  final end = DateTime.now().add(const Duration(seconds: 25));
  while (!ready()) {
    if (DateTime.now().isAfter(end)) throw TestFailure('Kafka UI timeout');
    await t.pump(const Duration(milliseconds: 100));
  }
}

Future<void> tap(WidgetTester t, Finder f) async {
  await wait(t, () => f.evaluate().isNotEmpty);
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await t.pump(const Duration(milliseconds: 400));
}

Future<void> enter(WidgetTester t, String key, String value) async {
  final f = find.byKey(ValueKey(key));
  await wait(t, () => f.evaluate().isNotEmpty);
  await t.ensureVisible(f);
  await t.pump();
  await t.enterText(f, value);
  await t.pump();
}

Future<void> back(WidgetTester t) async {
  await t.pageBack();
  await t.pump(const Duration(milliseconds: 400));
}

Future<void> run(
  WidgetTester t,
  Engine e, {
  bool write = true,
  bool cancel = false,
}) async {
  final previous = mapOf(await e.command({'op': 'state'}))['run_id'];
  await tap(t, find.byKey(const ValueKey('run_request')));
  if (write) {
    await wait(
      t,
      () => find.byKey(const ValueKey('confirm_action')).evaluate().isNotEmpty,
    );
    await tap(
      t,
      cancel ? find.text('取消') : find.byKey(const ValueKey('confirm_action')),
    );
    if (cancel) {
      expect(mapOf(await e.command({'op': 'state'}))['run_id'], previous);
      return;
    }
  }
  final end = DateTime.now().add(const Duration(seconds: 25));
  while (true) {
    final state = mapOf(await e.command({'op': 'state'}));
    if (state['run_id'] != previous && state['running'] == false) break;
    if (DateTime.now().isAfter(end))
      throw TestFailure('Kafka execution timeout');
    await t.pump(const Duration(milliseconds: 100));
  }
  await wait(t, () => find.text('已完成').evaluate().isNotEmpty);
}

Future<void> openRequest(WidgetTester t, String name) async {
  final search = find.byKey(const ValueKey('request_search'));
  await wait(t, () => search.evaluate().isNotEmpty);
  await t.enterText(search, name);
  await t.pump(const Duration(milliseconds: 200));
  await tap(t, find.widgetWithText(InkWell, name));
}
