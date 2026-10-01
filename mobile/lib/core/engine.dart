import 'dart:convert';
import 'package:flutter/services.dart';
import 'package:flutter/foundation.dart';
import 'json.dart';

abstract interface class Engine {
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
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
  Object? unwrap(Object? raw) {
    final reply = mapOf(raw is String ? jsonDecode(raw) : raw);
    if (reply['ok'] != true)
      throw EngineException(reply['error']?.toString() ?? '内核返回无效响应');
    return reply['data'];
  }

  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) async => mapOf(
    unwrap(
      await channel.invokeMethod('open', {
        'readOnly': readOnly,
        'history': history,
        if (path != null) 'path': path,
      }),
    ),
  );
  @override
  Future<Object?> command(JsonMap command) async {
    final raw = await channel.invokeMethod('command', {
      'json': exactEncode(command),
    });
    if (raw is String && raw.length > 262144) {
      return unwrap(await compute(_decodeLargeReply, raw));
    }
    return unwrap(raw);
  }

  @override
  Future<void> pause() async {
    await channel.invokeMethod('pause');
  }

  @override
  Future<void> resume() async {
    await channel.invokeMethod('resume');
  }

  @override
  Future<void> close() async {
    await channel.invokeMethod('close');
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

Object? _decodeLargeReply(String source) => jsonDecode(source);
