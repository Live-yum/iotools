import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'package:web/web.dart' as web;
import '../engine.dart';
import '../json.dart';
import 'gateway.dart';

class BrowserGatewayFiles implements GatewayFiles {
  BrowserGatewayFiles(
    this.transport, {
    void Function(Uri, String)? startDownload,
  }) : startDownload = startDownload ?? _downloadAnchor;
  final GatewayTransport transport;
  final void Function(Uri, String) startDownload;
  web.XMLHttpRequest? _upload;
  web.HTMLInputElement? _picker;
  Completer<web.File?>? _selection;
  bool _busy = false;
  int _generation = 0;
  int _limit(JsonMap args) {
    final value = int.tryParse('${args['limit'] ?? 1073741824}');
    if (value == null || value < 1 || value > 8589934592)
      throw const EngineException('文件大小上限无效');
    return value;
  }

  @override
  Future<Object?> pickAndUpload(bool bundle, JsonMap args) async {
    if (_busy) throw const EngineException('已有文件操作正在进行');
    final maximum = _limit(args);
    _busy = true;
    final generation = ++_generation;
    final selection = Completer<web.File?>();
    _selection = selection;
    final picker = web.HTMLInputElement()
      ..type = 'file'
      ..multiple = false
      ..accept = bundle ? '.zip' : '';
    picker.style.display = 'none';
    _picker = picker;
    picker.addEventListener(
      'change',
      ((web.Event event) {
        if (!selection.isCompleted) selection.complete(picker.files?.item(0));
      }).toJS,
    );
    picker.addEventListener(
      'cancel',
      ((web.Event event) {
        if (!selection.isCompleted) selection.complete(null);
      }).toJS,
    );
    web.document.body!.appendChild(picker);
    try {
      picker.click();
      final file = await selection.future;
      picker.remove();
      if (file == null || generation != _generation) return null;
      if (file.size > maximum) throw const EngineException('所选文件超过允许大小');
      final headers = await transport.headers();
      if (generation != _generation) return null;
      final uri = transport
          .endpoint('/api/files/upload')
          .replace(
            queryParameters: {
              'name': file.name,
              'bundle': bundle ? '1' : '0',
              'limit': '$maximum',
            },
          );
      final done = Completer<Object?>();
      final request = web.XMLHttpRequest()..open('POST', uri.toString(), true);
      _upload = request;
      request.setRequestHeader('X-Iotools-CSRF', headers['X-Iotools-CSRF']!);
      request.addEventListener(
        'load',
        ((web.Event event) {
          if (done.isCompleted) return;
          if (generation != _generation) {
            done.complete(null);
            return;
          }
          try {
            final reply = transport.envelope(request.responseText);
            if (request.status < 200 || request.status >= 300)
              throw EngineException(reply['error']?.toString() ?? '上传失败');
            done.complete(transport.unwrap(reply));
          } catch (error, stack) {
            done.completeError(error, stack);
          }
        }).toJS,
      );
      request.addEventListener(
        'error',
        ((web.Event event) {
          if (!done.isCompleted)
            done.completeError(const EngineException('上传中断，本机网关将清理临时文件'));
        }).toJS,
      );
      request.addEventListener(
        'abort',
        ((web.Event event) {
          if (!done.isCompleted) done.complete(null);
        }).toJS,
      );
      request.send(file);
      return await done.future;
    } finally {
      picker.remove();
      _picker = null;
      _selection = null;
      _upload = null;
      _busy = false;
    }
  }

  @override
  Future<Object?> download(JsonMap args) async {
    if (_busy) throw const EngineException('已有文件操作正在进行');
    final limit = _limit(args);
    if (args['text'] is String &&
        utf8.encode(args['text'] as String).length > limit)
      throw const EngineException('文本超过导出上限');
    _busy = true;
    final generation = ++_generation;
    try {
      final data = mapOf(
        await transport.post('/api/files/download', {...args, 'limit': limit}),
      );
      if (generation != _generation) return null;
      final path = data['url']?.toString() ?? '';
      if (!RegExp(r'^/api/download/[A-Za-z0-9_-]+$').hasMatch(path))
        throw const EngineException('网关返回无效下载票据');
      final uri = transport.endpoint(path);
      startDownload(uri, args['name']?.toString() ?? 'iotools-export');
      return {'download_started': true};
    } finally {
      _busy = false;
    }
  }

  @override
  Future<void> cancel() async {
    _generation++;
    if (_selection != null && !_selection!.isCompleted)
      _selection!.complete(null);
    _picker?.remove();
    _upload?.abort();
  }
}

void _downloadAnchor(Uri uri, String name) {
  final anchor = web.HTMLAnchorElement()
    ..href = uri.toString()
    ..download = name
    ..rel = 'noopener';
  web.document.body!.appendChild(anchor);
  anchor.click();
  anchor.remove();
}
