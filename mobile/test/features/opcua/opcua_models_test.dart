import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/opcua/opcua_models.dart';
import 'package:iotools_mobile/features/opcua/opcua_controller.dart';

UaMap base({
  String endpoint = 'opc.tcp://127.0.0.1:48410',
  String password = '',
}) => {
  'id': 'test',
  'protocol': 'opcua',
  'action': 'browse',
  'endpoint': endpoint,
  'params': {'node_id': 'i=85', 'password': password},
};
void main() {
  test(
    'gopcua discovery and history mode names become canonical request modes',
    () {
      expect(uaSecurityMode('MessageSecurityModeNone'), 'None');
      expect(uaSecurityMode('MessageSecurityModeSign'), 'Sign');
      expect(
        uaSecurityMode('MessageSecurityModeSignAndEncrypt'),
        'SignAndEncrypt',
      );
      expect(uaSecurityMode('SignAndEncrypt'), 'SignAndEncrypt');
      expect(
        uaSecurityMode('MessageSecurityModeInvalid'),
        'MessageSecurityModeInvalid',
      );
    },
  );

  test(
    'exact typed scalars, structured fields, ByteString and Byte array stay distinct',
    () {
      expect(
        uaValidate('UInt64', '18446744073709551615'),
        '18446744073709551615',
      );
      expect(
        () => uaValidate('UInt64', '18446744073709551616'),
        throwsFormatException,
      );
      expect(
        uaValidate('Int64', '-9223372036854775808'),
        '-9223372036854775808',
      );
      expect(() => uaValidate('Int32', '2147483648'), throwsFormatException);
      expect(() => uaValidate('Double', 'NaN'), throwsFormatException);
      expect(() => uaValidate('Float', '3.5e38'), throwsFormatException);
      expect(uaValidate('ByteString', '/wA='), '/wA=');
      expect(uaValidate('Byte[]', ['255', '0']), ['255', '0']);
      expect(() => uaValidate('Byte[]', '/wA='), throwsFormatException);
      expect(
        uaValidate('LocalizedText', {'text': '中文 🙂', 'locale': 'zh-CN'}),
        {'text': '中文 🙂', 'locale': 'zh-CN'},
      );
      expect(uaValidate('QualifiedName', {'name': '温度', 'namespace': '2'}), {
        'name': '温度',
        'namespace': 2,
      });
    },
  );
  test('node IDs enforce namespace and full numeric/opaque formats', () {
    uaNodeId('ns=65535;s=工厂/温度');
    uaNodeId('i=4294967295');
    expect(() => uaNodeId('ns=65536;s=x'), throwsFormatException);
    expect(() => uaNodeId('i=4294967296'), throwsFormatException);
    expect(
      () => uaNodeId('g=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaa--'),
      throwsFormatException,
    );
  });
  test(
    'cache isolates credentials and endpoints, bounds navigation and rows',
    () {
      final cache = UaCache();
      final a = cache.visit(base(), 'i=85');
      a.browse.add({'node_id': 'i=1'});
      final b = cache.visit(base(password: 'changed'), 'i=85');
      expect(b.browse, isEmpty);
      cache.back();
      expect(cache.current, same(a));
      cache.forward();
      expect(cache.current, same(b));
      for (var i = 0; i < 300; i++) {
        cache.visit(base(), 'i=$i');
      }
      expect(cache.nodes.length, lessThanOrEqualTo(32));
      expect(cache.history.length, lessThanOrEqualTo(256));
      for (var i = 0; i < 2001; i++) {
        cache.current.add(cache.current.references, {'node_id': 'i=$i'});
      }
      expect(cache.current.references.length, 2000);
      expect(cache.current.truncated, isTrue);
    },
  );
  test('operation copies connection fields without replaying old writes', () {
    final request = base();
    request['params'] = {
      ...uaMap(request['params']),
      'value': '99',
      'arguments': [1],
      'object_id': 'i=1',
    };
    final op = uaOperation(request, 'i=42', 'read');
    expect(uaMap(op['params'])['value'], isNull);
    expect(uaMap(op['params'])['node_id'], 'i=42');
  });
  test(
    'preview cancellation, local read-only and one-use start never repeat writes',
    () async {
      final stream = StreamController<UaMap>.broadcast();
      final calls = <UaMap>[];
      Future<dynamic> command(UaMap c) async {
        calls.add(c);
        if (c['op'] == 'preview')
          return {'token': 'one', 'request': c['request'], 'mutates': true};
        if (c['op'] == 'run') return {'run_id': 'r1'};
        return {};
      }

      final controller = UaController(
        command: command,
        events: stream.stream,
        request: base(),
        readOnly: true,
      );
      final write = uaOperation(base(), 'i=85', 'write');
      await expectLater(controller.preview(write), throwsStateError);
      expect(calls, isEmpty);
      controller.readOnly = false;
      var preview = await controller.preview(write);
      controller.discard(preview!);
      expect(await controller.start(preview), isFalse);
      expect(calls.where((c) => c['op'] == 'run'), isEmpty);
      preview = await controller.preview(write);
      expect(await controller.start(preview!), isTrue);
      expect(await controller.start(preview), isFalse);
      expect(calls.where((c) => c['op'] == 'run').length, 1);
      controller.dispose();
      await stream.close();
    },
  );
  test(
    'background invalidates pending previews and stops subscriptions without replay',
    () async {
      final stream = StreamController<UaMap>.broadcast();
      final calls = <UaMap>[];
      final pending = Completer<dynamic>();
      Future<dynamic> command(UaMap c) async {
        calls.add(c);
        return c['op'] == 'preview'
            ? pending.future
            : c['op'] == 'subscriptions.list'
            ? []
            : {};
      }

      final model = UaController(
        command: command,
        events: stream.stream,
        request: base(),
        readOnly: false,
      );
      final p = model.preview(uaOperation(base(), 'i=85', 'read'));
      await model.background();
      pending.complete({'token': 'stale', 'request': base()});
      expect(await p, isNull);
      model.resume();
      await Future<void>.delayed(Duration.zero);
      expect(calls.where((c) => c['op'] == 'run'), isEmpty);
      expect(calls.where((c) => c['op'] == 'subscriptions.stop-all').length, 1);
      model.dispose();
      await stream.close();
    },
  );
  test(
    'late event routes to original node, duplicate sequence ignored',
    () async {
      final stream = StreamController<UaMap>.broadcast();
      Future<dynamic> command(UaMap c) async => c['op'] == 'preview'
          ? {'token': 'p', 'request': c['request']}
          : {'run_id': 'r'};
      final model = UaController(
        command: command,
        events: stream.stream,
        request: base(),
        readOnly: false,
      );
      final source = model.current;
      final preview = await model.preview(
        uaOperation(base(), 'i=85', 'attributes'),
      );
      await model.start(preview!);
      model.visit(base(), 'i=1');
      model.accept({
        'run_id': 'r',
        'seq': 1,
        'kind': 'attribute',
        'data': {'node_id': 'i=85', 'attribute': 'Value', 'value': 99},
      });
      model.accept({
        'run_id': 'r',
        'seq': 1,
        'kind': 'attribute',
        'data': {'node_id': 'i=85', 'attribute': 'Value', 'value': 100},
      });
      expect(model.current.attributes, isEmpty);
      expect(source.attributes['Value']!['value'], 99);
      model.accept({
        'run_id': 'r',
        'seq': 2,
        'kind': 'done',
        'data': {'status': 'completed'},
      });
      expect(model.foreground, isEmpty);
      model.dispose();
      await stream.close();
    },
  );
  test('editable structured caches preserve fields and opaque bytes', () {
    expect(uaEditable('LocalizedText', {'Text': '中文', 'Locale': 'zh-CN'}), {
      'text': '中文',
      'locale': 'zh-CN',
    });
    expect(
      uaEditable('QualifiedName[]', [
        {'Name': '压力', 'NamespaceIndex': 3},
      ]),
      [
        {'name': '压力', 'namespace': 3},
      ],
    );
    expect(uaEditable('Byte[]', '/wA='), ['255', '0']);
    expect(
      () => uaValidate('DateTime', '2026-02-30T12:00:00Z'),
      throwsFormatException,
    );
    expect(
      uaValidate('DateTime', '2024-02-29T12:00:00+08:00'),
      '2024-02-29T12:00:00+08:00',
    );
  });
}
