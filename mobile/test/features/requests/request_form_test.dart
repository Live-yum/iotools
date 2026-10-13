import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/requests/request_form.dart';

import '../../support/fake_engine.dart';

const _batchKey = ValueKey('param_read_batch_size');
const _batchError = '请输入 1..1000 的整数；留空使用默认值 100';

Future<void> _openForm(
  WidgetTester tester, {
  String action = 'read',
  JsonMap params = const {'read_batch_size': 100},
  required ValueChanged<JsonMap> onChanged,
}) async {
  final session = AppSession(FakeEngine(), FakePlatform());
  addTearDown(session.dispose);
  session.catalog = {
    'protocols': [
      {
        'id': 'opcua',
        'actions': [
          for (final action in ['browse', 'read', 'attributes'])
            {
              'id': action,
              'name': action,
              'defaults': {if (action != 'browse') 'read_batch_size': 100},
            },
        ],
        'fields': [
          {
            'key': 'read_batch_size',
            'type': 'number',
            'label': '每批读取属性数量 · read_batch_size',
            'hint': '每批读取的属性数量；默认 100，整数范围 1..1000',
          },
          {'key': 'max_nodes', 'type': 'number', 'label': '扫描节点数量上限'},
        ],
      },
    ],
  };
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: SingleChildScrollView(
          child: RequestForm(
            session: session,
            request: {
              'id': 'read',
              'protocol': 'opcua',
              'action': action,
              'endpoint': 'opc.tcp://127.0.0.1:4840',
              'params': params,
            },
            onChanged: onChanged,
          ),
        ),
      ),
    ),
  );
  final advanced = find.text('连接、安全与高级参数');
  await tester.ensureVisible(advanced);
  await tester.tap(advanced);
  await tester.pumpAndSettle();
  await tester.ensureVisible(find.byKey(_batchKey));
}

TextField _batch(WidgetTester tester) =>
    tester.widget<TextField>(find.byKey(_batchKey));

Future<void> _enterBatch(WidgetTester tester, String value) async {
  await tester.enterText(find.byKey(_batchKey), value);
  await tester.pump();
}

void main() {
  testWidgets('OPC UA batch size displays default and accepts integer bounds', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(tester, onChanged: (value) => changed = value);
    expect(_batch(tester).controller!.text, '100');
    expect(_batch(tester).keyboardType, TextInputType.number);
    expect(_batch(tester).decoration!.errorText, isNull);
    expect(_batch(tester).decoration!.helperText, contains('默认 100'));
    expect(changed, isNull);

    for (final value in ['1', '1000', '100', '+100', '00100']) {
      await _enterBatch(tester, value);
      expect(_batch(tester).decoration!.errorText, isNull);
      expect(mapOf(changed!['params'])['read_batch_size'], value);
    }
  });

  testWidgets('invalid batch edits stay visible and reach engine validation', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(
      tester,
      params: {'read_batch_size': 100, 'max_nodes': 200},
      onChanged: (value) => changed = value,
    );
    for (final value in [
      '0',
      '-1',
      '1001',
      '1.5',
      '100.0',
      '1e2',
      '0x64',
      ' 100 ',
      'abc',
      '9223372036854775808',
    ]) {
      await _enterBatch(tester, value);
      expect(_batch(tester).controller!.text, value);
      expect(_batch(tester).decoration!.errorText, _batchError);
      expect(mapOf(changed!['params'])['read_batch_size'], value);
      expect(mapOf(changed!['params'])['max_nodes'], 200);
    }

    await _enterBatch(tester, '250');
    expect(_batch(tester).decoration!.errorText, isNull);
    expect(mapOf(changed!['params'])['read_batch_size'], '250');
    await _enterBatch(tester, '');
    expect(_batch(tester).decoration!.errorText, isNull);
    expect(mapOf(changed!['params']).containsKey('read_batch_size'), isFalse);
    expect(mapOf(changed!['params'])['max_nodes'], 200);
  });

  testWidgets('initial invalid batch value is shown without replacing it', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(
      tester,
      params: {'read_batch_size': 1001},
      onChanged: (value) => changed = value,
    );
    expect(_batch(tester).controller!.text, '1001');
    expect(_batch(tester).decoration!.errorText, _batchError);
    expect(changed, isNull);
  });

  testWidgets('imported integer-valued numbers remain valid until edited', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(
      tester,
      params: {'read_batch_size': const ExactNumber('100.0')},
      onChanged: (value) => changed = value,
    );
    expect(_batch(tester).controller!.text, '100.0');
    expect(_batch(tester).decoration!.errorText, isNull);
    expect(changed, isNull);
    await _enterBatch(tester, '100');
    await _enterBatch(tester, '100.0');
    expect(_batch(tester).decoration!.errorText, _batchError);
    expect(mapOf(changed!['params'])['read_batch_size'], '100.0');
  });

  testWidgets('batch templates and other numeric fields keep form behavior', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(tester, onChanged: (value) => changed = value);
    for (final value in [r'${batch_size}', '{{ env("OPCUA_READ_BATCH") }}']) {
      await _enterBatch(tester, value);
      expect(_batch(tester).decoration!.errorText, isNull);
      expect(mapOf(changed!['params'])['read_batch_size'], value);
    }
    final nodes = find.byKey(const ValueKey('param_max_nodes'));
    await tester.ensureVisible(nodes);
    await tester.enterText(nodes, r'${nodes}');
    await tester.pump();
    expect(mapOf(changed!['params'])['max_nodes'], r'${nodes}');
    expect(tester.widget<TextField>(nodes).decoration!.errorText, isNull);
  });

  testWidgets('read action defaults update an already-mounted batch field', (
    tester,
  ) async {
    JsonMap? changed;
    await _openForm(
      tester,
      action: 'browse',
      params: {},
      onChanged: (value) => changed = value,
    );
    expect(_batch(tester).controller!.text, isEmpty);
    final action = find.byKey(const ValueKey('opcua/browse'));
    await tester.ensureVisible(action);
    await tester.tap(action);
    await tester.pumpAndSettle();
    await tester.tap(find.text('read').last);
    await tester.pumpAndSettle();
    expect(_batch(tester).controller!.text, '100');
    expect(mapOf(changed!['params'])['read_batch_size'], 100);

    await tester.ensureVisible(find.byKey(_batchKey));
    await _enterBatch(tester, '250');
    final readAction = find.byKey(const ValueKey('opcua/read'));
    await tester.ensureVisible(readAction);
    await tester.tap(readAction);
    await tester.pumpAndSettle();
    await tester.tap(find.text('attributes').last);
    await tester.pumpAndSettle();
    expect(_batch(tester).controller!.text, '250');
    expect(mapOf(changed!['params'])['read_batch_size'], '250');
  });
}
