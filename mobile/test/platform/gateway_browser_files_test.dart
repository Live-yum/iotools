@TestOn('browser')
library;

import 'dart:async';
import 'dart:js_interop';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:web/web.dart' as web;
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/platform/gateway.dart';
import 'package:iotools_mobile/core/platform/gateway_files_web.dart';

void main() {
  test('browser picker cancel removes DOM without bootstrap/upload', () async {
    var requests = 0;
    final transport = GatewayTransport(
      origin: Uri.parse('http://localhost:9000'),
      client: MockClient((_) async {
        requests++;
        return http.Response('{}', 500);
      }),
    );
    final files = BrowserGatewayFiles(transport);
    final pending = files.pickAndUpload(false, {'limit': 4096});
    final input =
        web.document.querySelector('input[type=file]') as web.HTMLInputElement;
    input.dispatchEvent(web.Event('cancel'));
    expect(await pending, isNull);
    expect(requests, 0);
    expect(web.document.querySelector('input[type=file]'), isNull);
    final again = files.pickAndUpload(true, {'limit': 4096});
    await files.cancel();
    expect(await again, isNull);
    expect(web.document.querySelector('input[type=file]'), isNull);
  });
  test('invalid transfer size does not poison the next picker', () async {
    final transport = GatewayTransport(
      origin: Uri.parse('http://localhost:9000'),
      client: MockClient((_) async => http.Response('{}', 500)),
    );
    final files = BrowserGatewayFiles(transport);
    await expectLater(
      files.pickAndUpload(false, {'limit': -1}),
      throwsA(isA<EngineException>()),
    );
    final pending = files.pickAndUpload(false, {'limit': 4096});
    await files.cancel();
    expect(await pending, isNull);
  });
  test('browser file size is checked before reading or sending Blob', () async {
    var requests = 0;
    final transport = GatewayTransport(
      origin: Uri.parse('http://localhost:9000'),
      client: MockClient((_) async {
        requests++;
        return http.Response('{}', 500);
      }),
    );
    final files = BrowserGatewayFiles(transport);
    final pending = files.pickAndUpload(false, {'limit': 8});
    final expected = expectLater(pending, throwsA(isA<EngineException>()));
    final transfer = web.DataTransfer();
    transfer.items.add(web.File(['123456789'.toJS].toJS, 'large.bin'));
    final input =
        web.document.querySelector('input[type=file]') as web.HTMLInputElement;
    input.files = transfer.files;
    input.dispatchEvent(web.Event('change'));
    await expected;
    expect(requests, 0);
  });
  test(
    'download accepts only same-origin one-use ticket and reports started',
    () async {
      String ticket = '/api/download/public-test-ticket';
      Uri? destination;
      final transport = GatewayTransport(
        origin: Uri.parse('http://localhost:9000'),
        client: MockClient(
          (request) async => http.Response(
            request.url.path == '/api/bootstrap'
                ? '{"ok":true,"data":{"csrf":"nonce"}}'
                : '{"ok":true,"data":{"url":"$ticket"}}',
            200,
          ),
        ),
      );
      final files = BrowserGatewayFiles(
        transport,
        startDownload: (uri, name) {
          destination = uri;
          expect(name, '中文.txt');
        },
      );
      expect(await files.download({'text': '中文', 'name': '中文.txt'}), {
        'download_started': true,
      });
      expect(
        destination.toString(),
        'http://localhost:9000/api/download/public-test-ticket',
      );
      ticket = 'https://evil.example/api/download/ticket';
      destination = null;
      await expectLater(
        files.download({'text': '中文', 'name': '中文.txt'}),
        throwsA(isA<EngineException>()),
      );
      expect(destination, isNull);
    },
  );
  test('cancelled download preparation never clicks a late ticket', () async {
    final held = Completer<http.Response>();
    final started = Completer<void>();
    var clicks = 0;
    final transport = GatewayTransport(
      origin: Uri.parse('http://localhost:9000'),
      client: MockClient((request) async {
        if (request.url.path == '/api/bootstrap')
          return http.Response('{"ok":true,"data":{"csrf":"nonce"}}', 200);
        started.complete();
        return held.future;
      }),
    );
    final files = BrowserGatewayFiles(
      transport,
      startDownload: (_, __) => clicks++,
    );
    final pending = files.download({'text': 'cancel'});
    await started.future;
    await files.cancel();
    held.complete(
      http.Response('{"ok":true,"data":{"url":"/api/download/late"}}', 200),
    );
    expect(await pending, isNull);
    expect(clicks, 0);
  });
}
