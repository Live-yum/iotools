import 'dart:convert';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';
import 'opcua_workflow_test.dart';
import 'modbus_workflow_test.dart';
import 'kafka_management_test.dart';

void main() {
  WidgetController.hitTestWarningShouldBeFatal = true;
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'Flutter HTTP native AES controls, exact wire bytes, history, SQL and privacy',
    (tester) async {
      const engine = MethodChannelEngine(), platform = MethodChannelPlatform();
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final received = <String>[];
      // Public independent OpenSSL AES-128-CBC/PKCS7 vectors, not calculated by the engine under test.
      const expected = 'qaecBiJg5qYkn1be0PS0WKhxPv6lLp69C1xwHFRMubE=';
      const responseCipher =
          'tPXT09vO41jl5nnDRzL/o2IXTqn+Ik2TLqQ7mHjsQGJVm+cnEb4WHA0oNdfc/Mam';
      server.listen((request) async {
        final raw = await utf8.decoder.bind(request).join();
        received.add(raw);
        request.response.headers.contentType = ContentType.text;
        switch (request.uri.path) {
          case '/aes':
            request.response.write(responseCipher);
          case '/binary':
            request.response.add([0, 255, 128]);
          case '/chinese':
            request.response.write('中文😀');
          default:
            request.response.write('test');
        }
        await request.response.close();
      });
      await platform.invoke('settings.save', {
        'theme': 'dark',
        'readOnly': false,
        'history': false,
        'collection': 'iotools.yaml',
      });
      await engine.close();
      await engine.open();
      final original = mapOf(await engine.command({'op': 'config.get'}));
      final endpoint = 'http://127.0.0.1:${server.port}';
      final requests = [
        {
          'id': 'aes',
          'name': 'Flutter AES 整体验收',
          'protocol': 'http',
          'action': 'POST',
          'endpoint': '$endpoint/aes',
          'timeout': '5s',
          'params': {'body': '工业 AES 请求 😀'},
        },
        for (final name in ['literal', 'chinese', 'binary'])
          {
            'id': name,
            'name': 'HTTP $name',
            'protocol': 'http',
            'action': 'GET',
            'endpoint': '$endpoint/$name',
            'timeout': '5s',
          },
      ];
      await engine.command({
        'op': 'config.save',
        'source': jsonEncode({
          'version': 1,
          'profiles': {'local': {}},
          'requests': requests,
        }),
      });
      await engine.close();
      try {
        await tester.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await waitFor(
          tester,
          () => find.text('Flutter AES 整体验收').evaluate().isNotEmpty,
        );
        await tapKey(tester, 'nav_settings');
        await tapKey(tester, 'history_opt_in');
        await tapText(tester, '开启');
        await tapKey(tester, 'nav_requests');
        await openRequest(tester, 'Flutter AES 整体验收');
        await shot(tester, binding, 'flutter-http-01-form');
        await tapKey(tester, 'add_codec');
        await tester.enterText(
          find.byKey(const ValueKey('input_dialog')),
          'aes',
        );
        await tapText(tester, '应用');
        await choose(tester, 'codec_algorithm', 'aes-128-cbc');
        await tester.enterText(
          find.byKey(const ValueKey('codec_key')),
          '0123456789abcdef',
        );
        await tester.enterText(
          find.byKey(const ValueKey('codec_iv')),
          'fedcba9876543210',
        );
        await shot(tester, binding, 'flutter-http-02-aes-editor');
        await tapKey(tester, 'codec_apply');
        await tapKey(tester, 'add_request_transform');
        await choose(tester, 'transform_type', 'encrypt');
        await tapKey(tester, 'transform_apply');
        await tapKey(tester, 'add_response_transform');
        await choose(tester, 'transform_type', 'decrypt');
        await tapKey(tester, 'transform_apply');
        await tapKey(tester, 'run_request');
        await waitFor(
          tester,
          () => find
              .byKey(const ValueKey('confirm_action'))
              .evaluate()
              .isNotEmpty,
        );
        await shot(tester, binding, 'flutter-http-03-write-review');
        await tapText(tester, '取消');
        expect(received, isEmpty);
        await tapKey(tester, 'run_request');
        await tapKey(tester, 'confirm_action');
        await waitFor(tester, () => find.text('已完成').evaluate().isNotEmpty);
        expect(received, [expected]);
        expect(find.textContaining('AES 解密成功中文'), findsWidgets);
        await shot(tester, binding, 'flutter-http-04-decrypted-result');
        for (final name in ['literal', 'chinese', 'binary']) {
          await tester.pageBack();
          await tester.pump(const Duration(milliseconds: 350));
          await openRequest(tester, 'HTTP $name');
          await tapKey(tester, 'run_request');
          await waitFor(tester, () => find.text('已完成').evaluate().isNotEmpty);
        }
        await tapKey(tester, 'nav_history');
        await waitFor(
          tester,
          () => find.textContaining('literal').evaluate().isNotEmpty,
        );
        await tapText(tester, 'GET literal');
        await waitFor(tester, () => find.text('test').evaluate().isNotEmpty);
        expect(find.text('test'), findsOneWidget);
        expect(
          find.textContaining(RegExp(r'20\d\d-.*T')).evaluate().isNotEmpty,
          true,
        );
        await shot(tester, binding, 'flutter-http-05-history-literal');
        await tapText(tester, '关闭');
        await tapText(tester, 'SQL 查询 / 事务');
        await tester.enterText(
          find.byKey(const ValueKey('sql_editor')),
          'SELECT 1 AS 编号, \'test\' AS 正文, \'中文😀\' AS 中文',
        );
        await tapKey(tester, 'sql_query');
        await waitFor(
          tester,
          () => find.byType(DataTable).evaluate().isNotEmpty,
        );
        await shot(tester, binding, 'flutter-http-06-sql-table');
        await tapText(tester, '关闭');
        await tapKey(tester, 'nav_settings');
        await tapKey(tester, 'open_yaml');
        const secret = 'UNSAVED_ONLY_秘密😀';
        await tester.enterText(
          find.byKey(const ValueKey('yaml_editor')),
          '${original['source']}\n# $secret',
        );
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await tester.pump();
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await tester.pump(const Duration(milliseconds: 400));
        expect(find.textContaining(secret), findsOneWidget);
        final stored = mapOf(await engine.command({'op': 'config.get'}));
        expect(stored['source'].toString(), isNot(contains(secret)));
        expect(
          tester
              .widget<TextField>(find.byKey(const ValueKey('yaml_editor')))
              .restorationId,
          isNull,
        );
      } finally {
        await tester.pumpWidget(const SizedBox());
        await tester.pump(const Duration(milliseconds: 400));
        await engine.open();
        await engine.command({
          'op': 'config.save',
          'source': original['source'],
        });
        await engine.close();
        await platform.invoke('settings.save', {
          'history': false,
          'readOnly': false,
        });
        await server.close(force: true);
      }
    },
  );
  testWidgets(
    'Flutter MQTT exact empty topic levels and Kafka raw records on real local fixtures',
    (tester) async {
      const engine = MethodChannelEngine(), platform = MethodChannelPlatform();
      await platform.invoke('settings.save', {
        'theme': 'dark',
        'readOnly': false,
        'history': false,
        'collection': 'iotools.yaml',
      });
      await engine.close();
      await engine.open();
      final original = mapOf(await engine.command({'op': 'config.get'}));
      await engine.command({
        'op': 'config.save',
        'source': jsonEncode({
          'version': 1,
          'profiles': {'local': {}},
          'requests': [
            {
              'id': 'mqtt',
              'name': 'Flutter MQTT 主题验收',
              'protocol': 'mqtt',
              'action': 'read-one',
              'endpoint': 'mqtt://127.0.0.1:48414',
              'timeout': '5s',
              'params': {'topic': '/sensors//temp/', 'qos': 0},
            },
            {
              'id': 'kafka',
              'name': 'Flutter Kafka 记录验收',
              'protocol': 'kafka',
              'action': 'consume',
              'endpoint': '127.0.0.1:48412',
              'timeout': '10s',
              'params': {
                'topic': 'mobile-records',
                'offset': 'earliest',
                'limit': 2,
              },
            },
          ],
        }),
      });
      await engine.close();
      try {
        await tester.pumpWidget(
          const IotoolsApp(engine: engine, platform: platform),
        );
        await waitFor(
          tester,
          () => find.text('Flutter MQTT 主题验收').evaluate().isNotEmpty,
        );
        await openRequest(tester, 'Flutter MQTT 主题验收');
        await tapKey(tester, 'run_request');
        await waitFor(tester, () => find.text('已完成').evaluate().isNotEmpty);
        expect(find.text('/sensors//temp/'), findsWidgets);
        await tapText(tester, '主题树、消息历史与图表');
        await shot(tester, binding, 'flutter-mqtt-01-exact-topic-history');
        expect(find.textContaining('全部主题'), findsWidgets);
        await tester.pageBack();
        await tester.pump(const Duration(milliseconds: 350));
        await tester.pageBack();
        await tester.pump(const Duration(milliseconds: 350));
        await openRequest(tester, 'Flutter Kafka 记录验收');
        await tapKey(tester, 'run_request');
        await waitFor(tester, () => find.text('已完成').evaluate().isNotEmpty);
        await tapText(tester, '浏览 Kafka 记录');
        expect(find.textContaining('fixture-first'), findsWidgets);
        await shot(tester, binding, 'flutter-kafka-01-record-browser');
        await tapText(tester, 'mobile-records / 0 / 0');
        expect(find.textContaining('raw_key_base64'), findsWidgets);
        expect(find.textContaining('/wA='), findsWidgets);
        await shot(tester, binding, 'flutter-kafka-02-binary-record');
      } finally {
        await tester.pumpWidget(const SizedBox());
        await tester.pump(const Duration(milliseconds: 400));
        await engine.open();
        await engine.command({
          'op': 'config.save',
          'source': original['source'],
        });
        await engine.close();
      }
    },
  );
  registerKafkaManagementTests();
  registerOpcuaIntegrationTests();
  registerModbusIntegrationTests();
}

Future<void> waitFor(
  WidgetTester tester,
  bool Function() predicate, {
  int seconds = 20,
}) async {
  final end = DateTime.now().add(Duration(seconds: seconds));
  while (!predicate()) {
    if (DateTime.now().isAfter(end)) throw TestFailure('等待 Flutter 工作流超时');
    await tester.pump(const Duration(milliseconds: 100));
  }
  await tester.pump(const Duration(milliseconds: 100));
}

Future<void> tapKey(WidgetTester t, String key) async {
  await waitFor(t, () => find.byKey(ValueKey(key)).evaluate().isNotEmpty);
  final f = find.byKey(ValueKey(key));
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await t.pump(const Duration(milliseconds: 400));
}

Future<void> tapText(WidgetTester t, String text) async {
  await waitFor(t, () => find.text(text).evaluate().isNotEmpty);
  final f = find.text(text).last;
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await t.pump(const Duration(milliseconds: 400));
}

Future<void> choose(WidgetTester t, String key, String item) async {
  await tapKey(t, key);
  await tapText(t, item);
}

bool _converted = false;
Future<void> shot(
  WidgetTester t,
  IntegrationTestWidgetsFlutterBinding b,
  String name,
) async {
  if (!_converted) {
    await b.convertFlutterSurfaceToImage();
    _converted = true;
    addTearDown(() => _converted = false);
  }
  await t.pump();
  await b.takeScreenshot(name);
}

Future<void> openRequest(WidgetTester t, String name) async {
  final f = find.byKey(const ValueKey('request_search'));
  await waitFor(t, () => f.evaluate().isNotEmpty);
  await t.enterText(f, name);
  await t.pump(const Duration(milliseconds: 200));
  await tapText(t, name);
}
