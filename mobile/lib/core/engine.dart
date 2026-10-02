import 'package:flutter/services.dart';
import 'package:flutter/foundation.dart';
import 'json.dart';

abstract interface class Engine {
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = true,
    String? path,
  });
  Future<Object?> command(JsonMap command);
  Future<void> pause();
  Future<void> resume();
  Future<void> close();
}

class EngineException implements Exception {
  const EngineException(this.message);
  final String message;
  @override
  String toString() => message;
}

class MethodChannelEngine implements Engine {
  const MethodChannelEngine([
    this.channel = const MethodChannel('io.github.liveyum.iotools/engine'),
  ]);
  final MethodChannel channel;
  // All engine handles share one native session. Preserve lifecycle order even
  // when Widget.dispose cannot await close and the host uses a worker pool.
  static Future<void> _lifecycle = Future<void>.value();
  Future<T> _ordered<T>(Future<T> Function() operation) {
    final result = _lifecycle.then((_) => operation());
    _lifecycle = result.then<void>(
      (_) {},
      onError: (Object _, StackTrace __) {},
    );
    return result;
  }

  Object? unwrap(Object? raw) {
    final reply = mapOf(raw is String ? decodeEngineReply(raw) : raw);
    if (reply['ok'] != true)
      throw EngineException(reply['error']?.toString() ?? '内核返回无效响应');
    return reply['data'];
  }

  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = true,
    String? path,
  }) => _ordered(
    () async => mapOf(
      unwrap(
        await channel.invokeMethod('open', {
          'readOnly': readOnly,
          'history': history,
          if (path != null) 'path': path,
        }),
      ),
    ),
  );
  @override
  Future<Object?> command(JsonMap command) async {
    const fixtureDiagnostics = bool.fromEnvironment('IOTOOLS_TEST_FIXTURES');
    final trace = fixtureDiagnostics && command['op'] == 'preview';
    final elapsed = trace ? (Stopwatch()..start()) : null;
    if (trace) debugPrint('OPC_PREVIEW dart-start');
    try {
      await _lifecycle;
      if (trace)
        debugPrint(
          'OPC_PREVIEW platform-dispatch ${elapsed!.elapsedMilliseconds}ms',
        );
      final raw = await channel.invokeMethod('command', {
        'json': exactEncode(command),
      });
      if (trace)
        debugPrint(
          'OPC_PREVIEW platform-reply ${elapsed!.elapsedMilliseconds}ms',
        );
      if (raw is String && raw.length > 262144) {
        return unwrap(await compute(_decodeLargeReply, raw));
      }
      return unwrap(raw);
    } finally {
      if (trace)
        debugPrint(
          'OPC_PREVIEW dart-complete ${elapsed!.elapsedMilliseconds}ms',
        );
    }
  }

  @override
  Future<void> pause() async {
    await _ordered(() => channel.invokeMethod<void>('pause'));
  }

  @override
  Future<void> resume() async {
    await _ordered(() => channel.invokeMethod<void>('resume'));
  }

  @override
  Future<void> close() async {
    await _ordered(() => channel.invokeMethod<void>('close'));
  }
}

abstract interface class PlatformServices {
  Future<Object?> invoke(String method, [JsonMap args = const {}]);
}

class MethodChannelPlatform implements PlatformServices {
  const MethodChannelPlatform();
  static const channel = MethodChannel('io.github.liveyum.iotools/platform');
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) =>
      channel.invokeMethod(method, args);
}

Object? _decodeLargeReply(String source) => decodeEngineReply(source);
