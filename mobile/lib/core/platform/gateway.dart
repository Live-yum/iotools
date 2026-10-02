import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;
import '../engine.dart';
import '../json.dart';

bool isLoopbackOrigin(Uri uri) =>
    (uri.scheme == 'http' || uri.scheme == 'https') &&
    uri.userInfo.isEmpty &&
    const [
      'localhost',
      '127.0.0.1',
      '::1',
      '[::1]',
    ].contains(uri.host.toLowerCase());

Object? decodeGatewayReply(String source) => decodeEngineReply(source);

class GatewayTransport {
  GatewayTransport({required Uri origin, required this.client})
    : origin = _checkedOrigin(origin);
  static Uri _checkedOrigin(Uri origin) {
    if (!isLoopbackOrigin(origin))
      throw const EngineException('Web 版请从本机 Go 网关打开；不支持远程或 URL 指定的网关');
    return Uri.parse(origin.origin);
  }

  final Uri origin;
  final http.Client client;
  Future<JsonMap>? _bootstrap;
  String _csrf = '';

  Uri endpoint(String path) {
    final result = origin.resolve(path);
    if (!path.startsWith('/api/') ||
        result.origin != origin.origin ||
        result.userInfo.isNotEmpty ||
        result.fragment.isNotEmpty)
      throw const EngineException('网关路径必须位于当前页面同源');
    return result;
  }

  Object? unwrap(JsonMap reply) {
    if (reply['ok'] != true)
      throw EngineException(reply['error']?.toString() ?? '网关返回无效响应');
    return reply['data'];
  }

  JsonMap envelope(String text) {
    if (utf8.encode(text).length > 16 * 1024 * 1024 + 65536)
      throw const EngineException('网关响应超过安全上限');
    return mapOf(decodeGatewayReply(text));
  }

  Future<JsonMap> bootstrap() => _bootstrap ??= _loadBootstrap();
  Future<JsonMap> _loadBootstrap() async {
    try {
      final response = await client.get(endpoint('/api/bootstrap'));
      final data = mapOf(unwrap(envelope(utf8.decode(response.bodyBytes))));
      if (response.statusCode != 200 ||
          data['csrf'] is! String ||
          (data['csrf'] as String).isEmpty)
        throw const EngineException('本机网关握手失败，请刷新页面');
      _csrf = data['csrf'] as String;
      return data;
    } catch (_) {
      _bootstrap = null;
      rethrow;
    }
  }

  Future<Map<String, String>> headers({String? session}) async {
    await bootstrap();
    return {
      'Content-Type': 'application/json; charset=utf-8',
      'X-Iotools-CSRF': _csrf,
      if (session != null) 'X-Iotools-Session': session,
    };
  }

  Future<JsonMap> postEnvelope(
    String path,
    Object? body, {
    String? session,
    bool raw = false,
  }) async {
    final encoded = raw ? body as String : exactEncode(body);
    final maximum = path == '/api/files/download'
        ? 64 * 1024 * 1024
        : 8 * 1024 * 1024;
    if (utf8.encode(encoded).length > maximum)
      throw EngineException('请求内容超过 ${maximum ~/ (1024 * 1024)} MiB 上限');
    final response = await client.post(
      endpoint(path),
      headers: await headers(session: session),
      body: utf8.encode(encoded),
    );
    final reply = envelope(utf8.decode(response.bodyBytes));
    if (response.statusCode < 200 || response.statusCode >= 300)
      throw EngineException(
        reply['error']?.toString() ?? '网关请求失败（${response.statusCode}）',
      );
    return reply;
  }

  Future<Object?> post(String path, Object? body, {String? session}) async =>
      unwrap(await postEnvelope(path, body, session: session));
}

class GatewayEngine implements Engine {
  GatewayEngine(this.transport);
  final GatewayTransport transport;
  String? _session;
  Future<void> _lifecycle = Future<void>.value();
  Future<T> _ordered<T>(Future<T> Function() action) {
    final value = _lifecycle.then((_) => action());
    _lifecycle = value.then<void>(
      (_) {},
      onError: (Object _, StackTrace __) {},
    );
    return value;
  }

  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) => _ordered(() async {
    if (_session != null) {
      final old = _session;
      _session = null;
      await transport.post('/api/lifecycle', {'action': 'close'}, session: old);
    }
    final reply = await transport.postEnvelope('/api/open', {
      'readOnly': readOnly,
      'history': history,
      if (path != null) 'path': path,
    });
    final data = mapOf(transport.unwrap(reply));
    if (reply['session'] is! String || (reply['session'] as String).isEmpty)
      throw const EngineException('网关未返回有效会话');
    _session = reply['session'] as String;
    return data;
  });
  @override
  Future<Object?> command(JsonMap command) async {
    await _lifecycle;
    final session = _session;
    if (session == null) throw const EngineException('请先打开工作区');
    return transport.unwrap(
      await transport.postEnvelope(
        '/api/command',
        exactEncode(command),
        session: session,
        raw: true,
      ),
    );
  }

  Future<void> _change(String action) => _ordered(() async {
    final session = _session;
    if (session == null) return;
    if (action == 'close') _session = null;
    await transport.post('/api/lifecycle', {
      'action': action,
    }, session: session);
  });
  @override
  Future<void> pause() => _change('pause');
  @override
  Future<void> resume() => _change('resume');
  @override
  Future<void> close() => _change('close');
}

abstract interface class GatewayFiles {
  Future<Object?> pickAndUpload(bool bundle, JsonMap args);
  Future<Object?> download(JsonMap args);
  Future<void> cancel();
}

class GatewayPlatform implements PlatformServices {
  GatewayPlatform(this.transport, this.files);
  final GatewayTransport transport;
  final GatewayFiles files;
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    switch (method) {
      case 'files.pick':
        return files.pickAndUpload(false, args);
      case 'files.importBundle':
        return files.pickAndUpload(true, args);
      case 'files.export':
        return files.download(args);
      case 'files.cancel':
        await files.cancel();
        return null;
      default:
        return transport.post('/api/platform', {
          'method': method,
          'args': args,
        });
    }
  }
}

class UnavailableGatewayEngine implements Engine {
  const UnavailableGatewayEngine();
  Never _fail() => throw const EngineException(
    '请通过本机 Go 网关提供的 localhost 地址打开 Web 版；浏览器不能直接连接原生 TCP 设备',
  );
  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) async => _fail();
  @override
  Future<Object?> command(JsonMap command) async => _fail();
  @override
  Future<void> pause() async {}
  @override
  Future<void> resume() async {}
  @override
  Future<void> close() async {}
}

class UnavailableGatewayPlatform implements PlatformServices {
  const UnavailableGatewayPlatform();
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    if (method == 'settings.get')
      return {
        'platform': 'web',
        'theme': 'dark',
        'capabilities': {
          'native_protocols': false,
          'file_import': false,
          'file_export': false,
          'usb': false,
          'serial': false,
        },
      };
    throw const EngineException('请先从本机 Go 网关打开应用');
  }
}
