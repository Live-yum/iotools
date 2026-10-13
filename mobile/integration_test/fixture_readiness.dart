import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// Warm up the disposable fixture's adb-reversed HTTP connection before any
/// protocol operation. Only transport failures on this read-only readiness GET
/// are retried; HTTP errors, bad JSON, metrics and business actions still fail.
Future<Map<String, dynamic>> waitForFixtureReady(
  Uri uri, {
  Duration timeout = const Duration(seconds: 15),
  Duration attemptTimeout = const Duration(seconds: 3),
  Duration retryDelay = const Duration(milliseconds: 250),
  void Function(int attempt, Object error)? onRetry,
}) async {
  if (uri.scheme != 'http' ||
      uri.host != '127.0.0.1' ||
      uri.path != '/ready' ||
      uri.hasQuery ||
      uri.hasFragment ||
      uri.userInfo.isNotEmpty) {
    throw ArgumentError.value(uri, 'uri', 'Expected loopback fixture /ready');
  }
  if (timeout <= Duration.zero ||
      attemptTimeout <= Duration.zero ||
      retryDelay < Duration.zero) {
    throw ArgumentError('Fixture readiness requires positive finite budgets');
  }
  final clock = Stopwatch()..start();
  for (var attempt = 1; ; attempt++) {
    final remaining = timeout - clock.elapsed;
    final budget = remaining < attemptTimeout ? remaining : attemptTimeout;
    final client = HttpClient()..connectionTimeout = budget;
    Object? transientError;
    StackTrace? transientStack;
    try {
      return await (() async {
        final request = await client.getUrl(uri);
        // Keep the readiness allowlist on the wire too: a redirected GET could
        // reach a metrics/business endpoint or a different host.
        request.followRedirects = false;
        final reply = await request.close();
        if (reply.statusCode != HttpStatus.ok) {
          throw StateError('Fixture /ready returned HTTP ${reply.statusCode}');
        }
        final decoded = jsonDecode(await utf8.decoder.bind(reply).join());
        if (decoded is! Map<String, dynamic>) {
          throw const FormatException('Fixture /ready must be a JSON object');
        }
        return decoded;
      })().timeout(budget);
    } catch (error, stack) {
      if (error is! SocketException &&
          error is! HttpException &&
          error is! TimeoutException) {
        rethrow;
      }
      transientError = error;
      transientStack = stack;
    } finally {
      // Future.timeout alone does not cancel the in-flight socket. Closing it
      // prevents failed attempts from overlapping or leaking into the test.
      client.close(force: true);
    }
    if (attempt == 5 || clock.elapsed + retryDelay >= timeout) {
      Error.throwWithStackTrace(transientError, transientStack);
    }
    onRetry?.call(attempt, transientError);
    await Future<void>.delayed(retryDelay);
    if (clock.elapsed >= timeout) {
      Error.throwWithStackTrace(transientError, transientStack);
    }
  }
}
