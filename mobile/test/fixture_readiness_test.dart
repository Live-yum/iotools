import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/fixture_readiness.dart';

void main() {
  Future<HttpServer> server() async {
    final value = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => value.close(force: true));
    return value;
  }

  Uri ready(HttpServer server) =>
      Uri.parse('http://127.0.0.1:${server.port}/ready');

  test('readiness retries a dropped header on a fresh connection', () async {
    final fixture = await server();
    var requests = 0;
    final retries = <int>[];
    fixture.listen((request) async {
      expect(request.method, 'GET');
      expect(request.uri.path, '/ready');
      requests++;
      if (requests <= 2) {
        (await request.response.detachSocket(writeHeaders: false)).destroy();
        return;
      }
      request.response.write(jsonEncode({'loopback_only': true}));
      await request.response.close();
    });
    final result = await waitForFixtureReady(
      ready(fixture),
      retryDelay: const Duration(milliseconds: 1),
      onRetry: (attempt, error) {
        expect(error, isA<HttpException>());
        retries.add(attempt);
      },
    );
    expect(result['loopback_only'], isTrue);
    expect(requests, 3);
    expect(retries, [1, 2]);
  });

  test(
    'a persistent broken transport still fails after five attempts',
    () async {
      final fixture = await server();
      var requests = 0;
      fixture.listen((request) async {
        requests++;
        (await request.response.detachSocket(writeHeaders: false)).destroy();
      });
      await expectLater(
        waitForFixtureReady(ready(fixture), retryDelay: Duration.zero),
        throwsA(isA<HttpException>()),
      );
      expect(requests, 5);
    },
  );

  test(
    'a stalled response cannot exceed the overall readiness budget',
    () async {
      final fixture = await server();
      var requests = 0;
      fixture.listen((request) {
        requests++;
        // Intentionally never send response headers.
      });
      final clock = Stopwatch()..start();
      await expectLater(
        waitForFixtureReady(
          ready(fixture),
          timeout: const Duration(milliseconds: 100),
          attemptTimeout: const Duration(seconds: 3),
        ),
        throwsA(isA<TimeoutException>()),
      );
      expect(requests, 1);
      expect(clock.elapsed, lessThan(const Duration(seconds: 2)));
    },
  );

  for (final status in [400, 500]) {
    test('HTTP $status is a real failure and is not retried', () async {
      final fixture = await server();
      var requests = 0;
      fixture.listen((request) async {
        requests++;
        request.response.statusCode = status;
        await request.response.close();
      });
      await expectLater(
        waitForFixtureReady(ready(fixture)),
        throwsA(isA<StateError>()),
      );
      expect(requests, 1);
    });
  }

  for (final status in [301, 302, 307, 308]) {
    test('HTTP $status never follows or retries a redirect', () async {
      final fixture = await server();
      var readyRequests = 0, targetRequests = 0;
      fixture.listen((request) async {
        if (request.uri.path == '/ready') {
          readyRequests++;
          request.response.statusCode = status;
          request.response.headers.set(HttpHeaders.locationHeader, '/write');
        } else {
          targetRequests++;
          request.response.write('{"loopback_only":true}');
        }
        await request.response.close();
      });
      await expectLater(
        waitForFixtureReady(ready(fixture)),
        throwsA(isA<StateError>()),
      );
      expect(readyRequests, 1);
      expect(targetRequests, 0);
    });
  }

  for (final body in ['not JSON', '[]', 'true']) {
    test('invalid readiness body $body is not retried', () async {
      final fixture = await server();
      var requests = 0;
      fixture.listen((request) async {
        requests++;
        request.response.write(body);
        await request.response.close();
      });
      await expectLater(
        waitForFixtureReady(ready(fixture)),
        throwsA(isA<FormatException>()),
      );
      expect(requests, 1);
    });
  }

  test(
    'readiness recovery cannot retry metrics or a business endpoint',
    () async {
      for (final path in ['/metrics', '/write', '/ready?mutate=true']) {
        await expectLater(
          waitForFixtureReady(Uri.parse('http://127.0.0.1:48411$path')),
          throwsArgumentError,
        );
      }
      await expectLater(
        waitForFixtureReady(Uri.parse('https://example.com/ready')),
        throwsArgumentError,
      );
    },
  );
}
