import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/platform/native/native_engine.dart';

void main() {
  const library = String.fromEnvironment('IOTOOLS_NATIVE_TEST_LIBRARY');
  TestWidgetsFlutterBinding.ensureInitialized();
  group(
    'real Go FFI library',
    () {
      late Directory root;
      late NativeFfiEngine engine;
      setUp(() async {
        root = await Directory.systemTemp.createTemp('iotools-ffi-中文-');
        engine = NativeFfiEngine(
          root: () async => root.path,
          libraryPath: library,
        );
      });
      tearDown(() async {
        await engine.close();
        await root.delete(recursive: true);
      });
      test(
        'first launch, UTF8 JSON, exact UInt64, and explicit missing recovery',
        () async {
          final state = await engine.open();
          expect(state['path'], 'iotools.yaml');
          expect(state['running'], false);
          expect(await File('${root.path}/iotools.yaml').exists(), true);
          final catalog = mapOf(await engine.command({'op': 'catalog'}));
          expect(
            rowsOf(catalog['protocols']).map((v) => v['id']),
            containsAll(['http', 'mqtt', 'kafka', 'modbus', 'opcua']),
          );
          final text = '工业😀 18446744073709551615';
          final converted = mapOf(
            await engine.command({
              'op': 'crypto.convert',
              'direction': 'encode',
              'data': text,
              'codec': {'algorithm': 'base64'},
            }),
          );
          expect(converted['text'], base64.encode(utf8.encode(text)));
          final saved = mapOf(
            await engine.command({
              'op': 'request.save',
              'request': {
                'id': 'utf8-exact',
                'name': '中文😀',
                'protocol': 'http',
                'action': 'POST',
                'endpoint': 'http://127.0.0.1:1/never-run',
                'params': {
                  'json': {
                    'counter': const ExactNumber('18446744073709551615'),
                  },
                },
              },
            }),
          );
          final request = rowsOf(
            saved['requests'],
          ).firstWhere((v) => v['id'] == 'utf8-exact');
          expect(request['name'], '中文😀');
          expect(
            mapOf(mapOf(request['params'])['json'])['counter'].toString(),
            '18446744073709551615',
          );
          expect(
            (await engine.command({'op': 'state'}) as Map)['running'],
            false,
          );
          await expectLater(
            engine.open(path: 'missing.yaml'),
            throwsA(isA<EngineException>()),
          );
          expect(await File('${root.path}/missing.yaml').exists(), false);
        },
      );
      test(
        'distinct handles isolate sessions; pause/resume never execute requests',
        () async {
          await engine.open();
          final secondRoot = await Directory.systemTemp.createTemp(
            'iotools-ffi-second-',
          );
          final other = NativeFfiEngine(
            root: () async => secondRoot.path,
            libraryPath: library,
          );
          try {
            await other.open(readOnly: true);
            await Future.wait([
              engine.pause(),
              other.command({'op': 'catalog'}),
            ]);
            expect(
              mapOf(await engine.command({'op': 'state'}))['paused'],
              true,
            );
            expect(
              mapOf(await other.command({'op': 'state'}))['paused'],
              false,
            );
            await engine.resume();
            expect(
              mapOf(await engine.command({'op': 'state'}))['running'],
              false,
            );
            await engine.close();
            await expectLater(
              engine.command({'op': 'state'}),
              throwsA(isA<EngineException>()),
            );
            expect(
              mapOf(await other.command({'op': 'state'}))['options'],
              containsPair('read_only', true),
            );
          } finally {
            await other.close();
            await secondRoot.delete(recursive: true);
          }
        },
      );
      test(
        'actual HTTP uses Go engine and cancellation does not replay',
        () async {
          final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
          var requests = 0;
          final slow = Completer<void>();
          final subscription = server.listen((request) async {
            requests++;
            if (request.uri.path == '/slow') {
              if (!slow.isCompleted) slow.complete();
              await Future<void>.delayed(const Duration(milliseconds: 300));
            }
            request.response.headers.contentType = ContentType.json;
            request.response.write('{"text":"工业😀"}');
            try {
              await request.response.close();
            } catch (_) {}
          });
          try {
            await engine.open();
            Future<String> execute(String path) async {
              final preview = mapOf(
                await engine.command({
                  'op': 'preview',
                  'request': {
                    'id': 'ffi-loopback',
                    'name': '本机 FFI 测试',
                    'protocol': 'http',
                    'action': 'GET',
                    'endpoint': 'http://127.0.0.1:${server.port}$path',
                    'timeout': '5s',
                  },
                }),
              );
              return mapOf(
                await engine.command({
                  'op': 'run',
                  'token': preview['token'],
                  'confirmed': false,
                }),
              )['run_id'].toString();
            }

            Future<List<JsonMap>> completion(String id) async {
              final events = <JsonMap>[];
              final deadline = DateTime.now().add(const Duration(seconds: 5));
              while (DateTime.now().isBefore(deadline)) {
                events.addAll(
                  rowsOf(
                    mapOf(await engine.command({'op': 'events'}))['events'],
                  ).where((event) => event['run_id'] == id),
                );
                if (events.any((event) => event['kind'] == 'done'))
                  return events;
                await Future<void>.delayed(const Duration(milliseconds: 10));
              }
              throw StateError('loopback request did not complete');
            }

            final first = await completion(await execute('/success'));
            expect(
              first.any((event) => pretty(event['data']).contains('工业😀')),
              true,
            );
            expect(requests, 1);
            final id = await execute('/slow');
            await slow.future.timeout(const Duration(seconds: 5));
            await engine.command({'op': 'cancel', 'run_id': id});
            final cancelled = await completion(id);
            expect(
              mapOf(
                cancelled.lastWhere((event) => event['kind'] == 'done')['data'],
              )['status'],
              'cancelled',
            );
            await engine.pause();
            await engine.resume();
            expect(
              mapOf(await engine.command({'op': 'state'}))['running'],
              false,
            );
            expect(requests, 2);
          } finally {
            await subscription.cancel();
            await server.close(force: true);
          }
        },
      );
      test(
        'config reload and unrelated edit preserve UInt64 JSON type on actual HTTP wire',
        () async {
          final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
          final captured = Completer<String>();
          final subscription = server.listen((request) async {
            final body = await utf8.decoder.bind(request).join();
            if (!captured.isCompleted) captured.complete(body);
            request.response.write('ok');
            await request.response.close();
          });
          try {
            await engine.open();
            await engine.command({
              'op': 'config.save',
              'source':
                  '''version: 1
requests:
  - id: exact-http
    name: before
    protocol: http
    action: POST
    endpoint: http://127.0.0.1:${server.port}/exact
    params:
      json:
        number: 18446744073709551615
        string: "18446744073709551615"
''',
            });
            final config = mapOf(await engine.command({'op': 'config.get'}));
            final request = cloneMap(
              rowsOf(mapOf(config['collection'])['requests']).single,
            )..['name'] = 'after';
            final body = mapOf(mapOf(request['params'])['json']);
            expect(body['number'], isA<ExactNumber>());
            expect(body['string'], isA<String>());
            final preview = mapOf(
              await engine.command({'op': 'preview', 'request': request}),
            );
            await engine.command({
              'op': 'run',
              'token': preview['token'],
              'confirmed': true,
            });
            final wire = await captured.future.timeout(
              const Duration(seconds: 5),
            );
            expect(wire, contains('"number":18446744073709551615'));
            expect(wire, contains('"string":"18446744073709551615"'));
            final decoded = mapOf(exactDecode(wire));
            expect(decoded['number'], isA<ExactNumber>());
            expect(decoded['string'], isA<String>());
          } finally {
            await subscription.cancel();
            await server.close(force: true);
          }
        },
      );
    },
    skip: library.isEmpty
        ? 'requires an explicitly supplied compiled Go library'
        : false,
  );
}
