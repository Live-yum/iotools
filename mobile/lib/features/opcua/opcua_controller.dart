import 'dart:async';
import 'dart:collection';
import 'package:flutter/foundation.dart';
import 'opcua_models.dart';

typedef UaCommand = Future<dynamic> Function(UaMap command);

class UaPreview {
  UaPreview(
    this.data,
    this.request,
    this.nodeKey,
    this.generation,
    this.serial,
    this.changedTarget,
  );
  final UaMap data, request;
  final String nodeKey;
  final int generation, serial;
  final bool changedTarget;
  bool used = false;
  bool get review =>
      changedTarget ||
      data['mutates'] == true ||
      data['confirmation_required'] == true ||
      request['action'] == 'subscribe';
}

class _Run {
  _Run(this.key, this.action);
  final String key, action;
  String resolvedKey = '';
  int sequence = 0;
  bool finished = false;
}

/// One poll consumer lives in the app host. This controller only observes its broadcast stream.
class UaController extends ChangeNotifier {
  UaController({
    required this.command,
    required Stream<UaMap> events,
    required UaMap request,
    required this.readOnly,
    this.onStarted,
  }) {
    cache.visit(request, '${uaMap(request['params'])['node_id'] ?? 'i=85'}');
    _subscription = events.listen(accept, onError: (Object e) => fail(e));
  }
  final UaCommand command;
  final ValueChanged<UaMap>? onStarted;
  bool readOnly;
  final cache = UaCache();
  final drafts = LinkedHashMap<String, UaMap>();
  final log = <String>[];
  final _runs = LinkedHashMap<String, _Run>();
  final _early = <UaMap>[];
  List<UaMap> subscriptions = [];
  List<UaMap> connections = [];
  UaMap? identity;
  String foreground = '', notice = '缓存工作台 · 点按读取或刷新才连接';
  bool pending = false,
      paused = false,
      _disposed = false,
      _refreshingSubscriptions = false;
  int generation = 0, serial = 0;
  late StreamSubscription<UaMap> _subscription;
  Timer? _subDebounce;
  UaNode get current => cache.current;
  bool get busy => pending || foreground.isNotEmpty;
  void changed() {
    if (!_disposed) notifyListeners();
  }

  void fail(Object error) {
    if (_disposed) return;
    pending = false;
    notice = error is FormatException ? error.message : error.toString();
    changed();
  }

  void visit(UaMap request, String id) {
    uaNodeId(id);
    cache.visit(request, id);
    notice = '已打开节点缓存；刷新才读取';
    changed();
  }

  void back() {
    cache.back();
    changed();
  }

  void forward() {
    cache.forward();
    changed();
  }

  void remember(String key, UaMap draft) {
    drafts.remove(key);
    drafts[key] = uaCopy(draft);
    while (drafts.length > 32) {
      drafts.remove(drafts.keys.first);
    }
  }

  Future<UaPreview?> preview(UaMap request, {UaNode? node}) async {
    if (paused || _disposed) throw StateError('后台已取消操作，请回到前台重新预览');
    if (pending || (foreground.isNotEmpty && request['action'] != 'subscribe'))
      throw StateError('已有任务，请等待完成或取消');
    if (readOnly && ['write', 'call'].contains(request['action']))
      throw StateError('只读保护禁止写入与方法调用');
    final target = node ?? current, ticket = generation, attempt = ++serial;
    pending = true;
    notice = '正在本机校验预览；尚未连接';
    changed();
    try {
      final data = uaMap(await command({'op': 'preview', 'request': request}));
      if (_disposed || paused || ticket != generation || attempt != serial)
        return null;
      final resolved = uaMap(data['request']), p = uaMap(resolved['params']);
      final changedTarget =
          (target.resolvedEndpoint.isNotEmpty &&
              target.resolvedEndpoint != resolved['endpoint']) ||
          (target.resolvedNode.isNotEmpty &&
              p['node_id'] != null &&
              target.resolvedNode != p['node_id']);
      return UaPreview(
        data,
        uaCopy(request),
        target.key,
        ticket,
        attempt,
        changedTarget,
      );
    } finally {
      if (!_disposed && ticket == generation && attempt == serial) {
        pending = false;
        changed();
      }
    }
  }

  void discard(UaPreview preview) {
    preview.used = true;
    if (serial == preview.serial) serial++;
    notice = '已取消预览；没有发送操作';
    changed();
  }

  Future<bool> start(UaPreview preview) async {
    if (preview.used ||
        paused ||
        _disposed ||
        preview.generation != generation ||
        preview.serial != serial)
      return false;
    if (readOnly && ['write', 'call'].contains(preview.request['action']))
      throw StateError('只读保护禁止写入与方法调用');
    preview.used = true;
    pending = true;
    changed();
    try {
      final data = uaMap(
        await command({
          'op': 'run',
          'token': preview.data['token'],
          'confirmed':
              preview.data['mutates'] == true ||
              preview.data['confirmation_required'] == true,
        }),
      );
      final id = '${data['run_id'] ?? ''}',
          background = data['background'] == true;
      if (_disposed || paused || preview.generation != generation) {
        await command(
          background
              ? {
                  'op': 'subscriptions.stop',
                  'subscription_id': data['subscription_id'],
                }
              : {'op': 'cancel', 'run_id': id},
        );
        return false;
      }
      onStarted?.call(data);
      notice = background ? '独立订阅已启动；后台会停止，返回不会重放' : '请求已启动；切换节点不改变执行目标';
      if (!background) {
        foreground = id;
        _runs[id] = _Run(preview.nodeKey, '${preview.request['action']}');
        while (_runs.length > 32) {
          _runs.remove(_runs.keys.first);
        }
        final node = cache.nodes[preview.nodeKey],
            resolved = uaMap(preview.data['request']);
        if (node != null) {
          if (preview.changedTarget) node.clear();
          node.resolvedEndpoint =
              '${resolved['endpoint'] ?? preview.request['endpoint']}';
          node.resolvedNode =
              '${uaMap(resolved['params'])['node_id'] ?? node.id}';
          node.begin('${preview.request['action']}');
        }
        final delayed = List<UaMap>.from(_early);
        _early.clear();
        for (final event in delayed) {
          accept(event);
        }
      } else {
        unawaited(refreshSubscriptions());
      }
      return true;
    } finally {
      if (!_disposed) {
        pending = false;
        changed();
      }
    }
  }

  Future<void> cancel() async {
    final id = foreground;
    if (id.isEmpty) return;
    await command({'op': 'cancel', 'run_id': id});
    notice = '已请求取消当前任务；独立订阅保留';
    changed();
  }

  Future<void> background() async {
    generation++;
    serial++;
    pending = false;
    paused = true;
    _subDebounce?.cancel();
    _subDebounce = null;
    final id = foreground;
    foreground = '';
    for (final run in _runs.values) {
      run.finished = true;
    }
    notice = '后台已停止任务与订阅；返回不会重放';
    changed();
    // Exact owned foreground cancellation; Go lifecycle also invalidates all previews.
    if (id.isNotEmpty) {
      try {
        await command({'op': 'cancel', 'run_id': id});
      } catch (_) {}
    }
    try {
      await command({'op': 'subscriptions.stop-all'});
    } catch (_) {}
  }

  void resume() {
    paused = false;
    notice = '已回到前台；请明确启动新的操作';
    changed();
    unawaited(refreshSubscriptions());
  }

  Future<void> refreshSubscriptions() async {
    if (_disposed || paused || _refreshingSubscriptions) return;
    final ticket = generation;
    _refreshingSubscriptions = true;
    try {
      final data = await command({'op': 'subscriptions.list'});
      if (!_disposed && !paused && ticket == generation) {
        subscriptions = (data is List ? data : []).map(uaMap).toList();
        changed();
      }
    } catch (e) {
      fail(e);
    } finally {
      _refreshingSubscriptions = false;
    }
  }

  Future<void> stopSubscription(String id) async {
    await command({'op': 'subscriptions.stop', 'subscription_id': id});
    await refreshSubscriptions();
  }

  Future<void> stopAll() async {
    await command({'op': 'subscriptions.stop-all'});
    await refreshSubscriptions();
  }

  Future<void> refreshConnections() async {
    final data = await command({'op': 'opcua.connections'});
    if (_disposed) return;
    connections = (data is List ? data : []).map(uaMap).take(100).toList();
    changed();
  }

  Future<void> clearConnections() async {
    await command({'op': 'opcua.connections.clear', 'confirmed': true});
    connections = [];
    changed();
  }

  Future<void> generateIdentity(String cert, String key, String uri) async {
    if (busy || paused) throw StateError('请先完成或取消当前任务');
    final ticket = generation;
    pending = true;
    changed();
    try {
      final result = uaMap(
        await command({
          'op': 'opcua.identity',
          'cert_path': cert,
          'key_path': key,
          'application_uri': uri,
          'confirmed': true,
        }),
      );
      final id = '${result['run_id']}';
      if (_disposed || paused || ticket != generation) {
        await command({'op': 'cancel', 'run_id': id});
        return;
      }
      foreground = id;
      onStarted?.call(result);
      notice = '正在生成新的客户端身份；可取消';
      _runs[id] = _Run(current.key, 'identity');
      identity = null;
      final delayed = List<UaMap>.from(_early);
      _early.clear();
      for (final event in delayed) {
        accept(event);
      }
    } finally {
      if (!_disposed) {
        pending = false;
        changed();
      }
    }
  }

  void accept(UaMap event) {
    if (_disposed || paused) return;
    final kind = '${event['kind']}', id = '${event['run_id'] ?? ''}';
    if (kind.startsWith('subscription.')) {
      _subDebounce ??= Timer(const Duration(milliseconds: 150), () {
        _subDebounce = null;
        unawaited(refreshSubscriptions());
      });
      return;
    }
    final run = _runs[id];
    if (run == null) {
      if (pending) {
        _early.add(uaCopy(event));
        if (_early.length > 64) _early.removeAt(0);
      }
      return;
    }
    if (run.finished) return;
    final seq = int.tryParse('${event['seq']}') ?? 0;
    if (seq > 0 && seq <= run.sequence) return;
    run.sequence = seq;
    final node = cache.nodes[run.key], data = uaMap(event['data']);
    if (node != null) {
      if (data['truncated'] == true) {
        node.truncated = true;
        node.largeResults.add({'kind': kind, ...data});
        if (node.largeResults.length > 8) node.largeResults.removeAt(0);
        changed();
        return; // An envelope is never a real attribute/reference/method result.
      }
      switch (kind) {
        case 'reference':
          final target = cache.nodes[run.resolvedKey] ?? node;
          target.add(
            run.action == 'references' ? target.references : target.browse,
            data,
          );
          break;
        case 'attribute':
        case 'value':
          if (data['node_id'] == null ||
              data['node_id'] == node.id ||
              data['node_id'] == node.resolvedNode) {
            node.attributes['${data['attribute'] ?? 'Value'}'] = uaCopy(data);
          }
          break;
        case 'endpoint':
          node.add(node.endpoints, data);
          break;
        case 'browse-path':
          node.path = '${data['path'] ?? ''}';
          break;
        case 'path-resolved':
          node.resolved = uaCopy(data);
          final resolvedId = '${data['node_id'] ?? ''}';
          if (resolvedId.isNotEmpty) {
            final target = cache.node(node.request, resolvedId);
            target.browse.clear();
            target.resolvedEndpoint = node.resolvedEndpoint;
            target.resolvedNode = resolvedId;
            run.resolvedKey = target.key;
          }
          break;
        case 'method-arguments':
          node.method = uaCopy(data);
          break;
        case 'method':
          node.methodResult = uaCopy(data);
          break;
        case 'identity':
          identity = uaCopy(data);
          break;
      }
    }
    if (kind == 'done') {
      run.finished = true;
      if (foreground == id) foreground = '';
      notice = data['status'] == 'completed'
          ? '任务完成 · 当前显示缓存'
          : data['status'] == 'cancelled'
          ? '任务已取消 · 缓存保留'
          : '任务失败 · ${data['error'] ?? ''}';
      node?.status = notice;
    }
    if (!['attribute', 'reference'].contains(kind)) {
      log.add(
        '${event['time'] ?? ''} · ${run.action} · $kind${kind == 'done' ? ' · ${data['status']}' : ''}',
      );
      if (log.length > 100) log.removeAt(0);
    }
    cache.trim();
    changed();
  }

  @override
  void dispose() {
    generation++;
    serial++;
    _disposed = true;
    _subDebounce?.cancel();
    unawaited(_subscription.cancel());
    if (foreground.isNotEmpty)
      unawaited(
        command({
          'op': 'cancel',
          'run_id': foreground,
        }).catchError((_) => <String, dynamic>{}),
      );
    super.dispose();
  }
}
