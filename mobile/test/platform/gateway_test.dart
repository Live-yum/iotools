import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/platform/gateway.dart';

class Files implements GatewayFiles {
  final calls = <String>[];
  @override
  Future<Object?> pickAndUpload(bool bundle, JsonMap args) async {
    calls.add(bundle ? 'bundle' : 'pick');
    return null;
  }

  @override
  Future<Object?> download(JsonMap args) async {
    calls.add('download');
    return {'download_started': true};
  }

  @override
  Future<void> cancel() async {
    calls.add('cancel');
  }
}

void main() {
  test(
    'large explicit text exports have a separate bounded JSON allowance',
    () async {
      var posts = 0;
      final transport = GatewayTransport(
        origin: Uri.parse('http://localhost:9000'),
        client: MockClient((request) async {
          if (request.url.path == '/api/bootstrap')
            return http.Response('{"ok":true,"data":{"csrf":"nonce"}}', 200);
          posts++;
          return http.Response(
            '{"ok":true,"data":{"url":"/api/download/ticket"}}',
            200,
          );
        }),
      );
      final body = 'x' * (8 * 1024 * 1024 + 1);
      await transport.post('/api/files/download', {
        'text': body,
        'limit': 16 * 1024 * 1024,
      });
      expect(posts, 1);
      await expectLater(
        transport.post('/api/command', {'op': 'preview', 'body': body}),
        throwsA(isA<EngineException>()),
      );
      expect(posts, 1);
    },
  );
  test(
    'only same-origin literal loopback is allowed, URL gateway hints ignored',
    () {
      for (final raw in [
        'http://localhost:9800/app?gateway=https://evil.test',
        'http://127.0.0.1:9000/',
        'http://[::1]:9000/',
      ]) {
        final uri = Uri.parse(raw);
        final transport = GatewayTransport(
          origin: uri,
          client: MockClient((_) async => http.Response('', 500)),
        );
        expect(transport.endpoint('/api/open').origin, uri.origin);
        expect(transport.endpoint('/api/open').query, isEmpty);
        expect(
          () => transport.endpoint('https://evil.test/api/open'),
          throwsA(isA<EngineException>()),
        );
      }
      for (final raw in [
        'https://gateway.example',
        'http://localhost.evil.test',
        'file:///tmp/app.html',
        'http://user@localhost:9000',
        'http://127.0.0.2',
      ]) {
        expect(
          () => GatewayTransport(
            origin: Uri.parse(raw),
            client: MockClient((_) async => http.Response('', 500)),
          ),
          throwsA(isA<EngineException>()),
        );
      }
    },
  );
  test(
    'bootstrap csrf and opaque session bind every protocol request',
    () async {
      final calls = <http.Request>[];
      final transport = GatewayTransport(
        origin: Uri.parse('http://localhost:9000/'),
        client: MockClient((request) async {
          calls.add(request);
          switch (request.url.path) {
            case '/api/bootstrap':
              return http.Response('{"ok":true,"data":{"csrf":"nonce"}}', 200);
            case '/api/open':
              return http.Response(
                '{"ok":true,"session":"opaque","data":{"running":false}}',
                200,
              );
            default:
              return http.Response('{"ok":true,"data":{}}', 200);
          }
        }),
      );
      final engine = GatewayEngine(transport);
      await engine.open(readOnly: true, path: '附件/中文.yaml');
      await engine.command({
        'op': 'preview',
        'value': const ExactNumber('18446744073709551615'),
        'text': '工业😀',
      });
      await engine.pause();
      await engine.resume();
      await engine.close();
      expect(calls.where((v) => v.method == 'GET'), hasLength(1));
      for (final request in calls.where((v) => v.method == 'POST'))
        expect(request.headers['X-Iotools-CSRF'], 'nonce');
      final command = calls.firstWhere((v) => v.url.path == '/api/command');
      expect(command.headers['X-Iotools-Session'], 'opaque');
      expect(command.body, contains('"value":18446744073709551615'));
      expect(command.body, contains('工业😀'));
      expect(
        calls
            .where((v) => v.url.path == '/api/lifecycle')
            .map((v) => mapOf(jsonDecode(v.body))['action']),
        ['pause', 'resume', 'close'],
      );
      await expectLater(
        engine.command({'op': 'run'}),
        throwsA(isA<EngineException>()),
      );
    },
  );
  test(
    'HTTP JSON numeric tokens survive config read, unrelated edits, preview and run',
    () async {
      final commands = <String>[];
      const config =
          '{"ok":true,"data":{"collection":{"requests":[{"id":"exact","name":"before","protocol":"http","action":"POST","endpoint":"http://127.0.0.1:1","params":{"json":{"number":18446744073709551615,"string":"18446744073709551615","decimal":1.234567890123456789}}}]}}}';
      final transport = GatewayTransport(
        origin: Uri.parse('http://127.0.0.1:9000/'),
        client: MockClient((request) async {
          if (request.url.path == '/api/bootstrap')
            return http.Response('{"ok":true,"data":{"csrf":"nonce"}}', 200);
          if (request.url.path == '/api/open')
            return http.Response(
              '{"ok":true,"session":"session","data":{}}',
              200,
            );
          commands.add(request.body);
          final op = mapOf(exactDecode(request.body))['op'];
          return http.Response(
            op == 'config.get'
                ? config
                : op == 'preview'
                ? '{"ok":true,"data":{"token":"review"}}'
                : '{"ok":true,"data":{"run_id":"run"}}',
            200,
          );
        }),
      );
      final engine = GatewayEngine(transport);
      await engine.open();
      final collection = mapOf(
        mapOf(await engine.command({'op': 'config.get'}))['collection'],
      );
      final request = cloneMap(rowsOf(collection['requests']).single)
        ..['name'] = 'after';
      final json = mapOf(mapOf(request['params'])['json']);
      expect(json['number'], isA<ExactNumber>());
      expect(json['string'], isA<String>());
      final preview = mapOf(
        await engine.command({'op': 'preview', 'request': request}),
      );
      await engine.command({
        'op': 'run',
        'token': preview['token'],
        'confirmed': true,
      });
      final sent = commands.firstWhere((v) => v.contains('"op":"preview"'));
      expect(sent, contains('"number":18446744073709551615'));
      expect(sent, contains('"string":"18446744073709551615"'));
      expect(sent, contains('"decimal":1.234567890123456789'));
      expect(sent, contains('"name":"after"'));
    },
  );
  test(
    'display doubles and preexisting protocol UInt64 strings keep current model compatibility',
    () {
      final reply = mapOf(
        decodeGatewayReply(
          '{"value":12.25,"u64":"18446744073709551615","offset":18446744073709551615}',
        ),
      );
      expect(reply['value'], 12.25);
      expect(reply['u64'], '18446744073709551615');
      expect(reply['offset'], '18446744073709551615');
      expect(() => jsonEncode(reply), returnsNormally);
    },
  );
  test(
    'file cancellation stays null and browser export never claims saved completion',
    () async {
      final files = Files();
      final transport = GatewayTransport(
        origin: Uri.parse('http://localhost:9000'),
        client: MockClient((_) async => http.Response('{}', 500)),
      );
      final platform = GatewayPlatform(transport, files);
      expect(
        await platform.invoke('files.pick', {'limit': 8589934592}),
        isNull,
      );
      await platform.invoke('files.cancel');
      expect(await platform.invoke('files.export', {'text': '中文😀'}), {
        'download_started': true,
      });
      expect(files.calls, ['pick', 'cancel', 'download']);
    },
  );
  test(
    'close proceeds while a command is in flight and never retries it',
    () async {
      final held = Completer<http.Response>();
      final received = Completer<void>();
      var commands = 0, closes = 0;
      final transport = GatewayTransport(
        origin: Uri.parse('http://localhost:9000'),
        client: MockClient((request) async {
          if (request.url.path == '/api/bootstrap')
            return http.Response('{"ok":true,"data":{"csrf":"nonce"}}', 200);
          if (request.url.path == '/api/open')
            return http.Response(
              '{"ok":true,"session":"session","data":{}}',
              200,
            );
          if (request.url.path == '/api/command') {
            commands++;
            received.complete();
            return held.future;
          }
          closes++;
          return http.Response('{"ok":true,"data":{}}', 200);
        }),
      );
      final engine = GatewayEngine(transport);
      await engine.open();
      final pending = engine.command({'op': 'events'});
      await received.future;
      await engine.close();
      expect(closes, 1);
      expect(commands, 1);
      held.complete(http.Response('{"ok":true,"data":{"events":[]}}', 200));
      await pending;
    },
  );
}
