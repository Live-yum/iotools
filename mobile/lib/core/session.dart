import 'dart:async';
import 'package:flutter/foundation.dart';
import 'engine.dart';
import 'json.dart';

/// Single owner of engine events. Drafts and result bodies remain in memory only.
class AppSession extends ChangeNotifier {
  AppSession(this.engine, this.platform);
  final Engine engine;
  final PlatformServices platform;
  JsonMap state = {}, catalog = {}, draft = {}, preferences = {};
  final Map<String, JsonMap> drafts = {};
  final Map<String, Map<String, JsonMap>> _collectionDrafts = {};
  final Map<String, String> _collectionSources = {};
  Future<Object?> Function(String, JsonMap)? transfer;
  String get currentCollection => state['path']?.toString() ?? 'iotools.yaml';
  final List<JsonMap> events = [];
  JsonMap? originalResultRequest, resultRequest;
  String? originalRequestId;
  JsonMap? resultOverrides;
  String resultRunId = '', status = '尚未执行', source = '', savedSource = '';
  String? error;
  bool ready = false, paused = false, polling = false, disposed = false;
  int dropped = 0, evicted = 0;
  Timer? _timer, _visualTimer;
  DateTime _lastVisual = DateTime.fromMillisecondsSinceEpoch(0);
  int _epoch = 0;
  final _stream = StreamController<JsonMap>.broadcast(sync: true);
  Stream<JsonMap> get eventStream => _stream.stream;
  bool get readOnly => mapOf(state['options'])['read_only'] == true;
  bool get history => mapOf(state['options'])['history'] == true;
  bool get running => state['running'] == true;
  List<JsonMap> get requests => rowsOf(state['requests']);
  Future<Object?> command(JsonMap c) => engine.command(c);
  Future<void> initialize() async {
    try {
      final settings = preferences = mapOf(
        await platform.invoke('settings.get'),
      );
      state = await engine.open(
        readOnly: settings['readOnly'] == true,
        history: settings['history'] == true,
      );
      final selected = settings['collection']?.toString();
      if (selected != null &&
          selected.isNotEmpty &&
          selected != 'iotools.yaml') {
        final p = mapOf(
          await command({
            'op': 'config.switch',
            'path': selected,
            'confirmed': false,
          }),
        );
        state = mapOf(
          await command({
            'op': 'config.switch',
            'path': selected,
            'token': p['token'],
            'confirmed': true,
          }),
        );
      }
      if (disposed) return;
      catalog = mapOf(await command({'op': 'catalog'}));
      await refreshSource();
      if (disposed) return;
      ready = true;
      error = null;
      _timer = Timer.periodic(const Duration(milliseconds: 250), (_) => poll());
      notifyListeners();
    } catch (e) {
      if (disposed) return;
      error = '$e';
      notifyListeners();
    }
  }

  Future<void> refreshSource() async {
    final c = mapOf(await command({'op': 'config.get'}));
    source = savedSource = c['source']?.toString() ?? '';
  }

  void prepare(JsonMap request) {
    originalRequestId = requests.any((r) => r['id'] == request['id'])
        ? request['id'].toString()
        : null;
    draft = cloneMap(request);
    drafts[draft['id'].toString()] = cloneMap(draft);
    notifyListeners();
  }

  void updateDraft(JsonMap request) {
    if (draft['id'] != request['id']) drafts.remove(draft['id']);
    draft = cloneMap(request);
    drafts[draft['id'].toString()] = cloneMap(draft);
  }

  void discardDraft(String id) {
    drafts.remove(id);
    if (draft['id'] == id)
      draft = cloneMap(requests.where((r) => r['id'] == id).firstOrNull ?? {});
    notifyListeners();
  }

  JsonMap choose(JsonMap request) {
    originalRequestId = requests.any((r) => r['id'] == request['id'])
        ? request['id'].toString()
        : null;
    draft = cloneMap(drafts[request['id']] ?? request);
    notifyListeners();
    return draft;
  }

  Future<void> saveRequest(JsonMap request) async {
    if (source != savedSource)
      throw const EngineException(
        'YAML 草稿尚未保存。请先保存或撤销 YAML 修改，再保存表单，以免覆盖独立编辑。',
      );
    state = mapOf(
      await command({
        'op': 'request.save',
        'request': request,
        if (originalRequestId != null) 'original_id': originalRequestId,
      }),
    );
    drafts.remove(originalRequestId);
    originalRequestId = request['id'].toString();
    drafts.remove(request['id']);
    draft = cloneMap(request);
    await refreshSource();
    notifyListeners();
  }

  Future<void> saveSource() async {
    state = mapOf(await command({'op': 'config.save', 'source': source}));
    savedSource = source;
    notifyListeners();
  }

  Future<JsonMap> preview(JsonMap request, {JsonMap? overrides}) async => mapOf(
    await command({
      'op': 'preview',
      'request': request,
      if (overrides != null) 'overrides': overrides,
    }),
  );
  Future<JsonMap> run(
    JsonMap request,
    JsonMap preview, {
    bool confirmed = false,
    JsonMap? overrides,
  }) async {
    if (paused) throw const EngineException('应用处于后台，请重新预览');
    final epoch = _epoch;
    final r = mapOf(
      await command({
        'op': 'run',
        'token': preview['token'],
        'confirmed': confirmed,
      }),
    );
    if (disposed || paused || epoch != _epoch) {
      if (r['run_id'] != null) {
        try {
          await command(
            r['background'] == true
                ? {
                    'op': 'subscriptions.stop',
                    'subscription_id': r['subscription_id'],
                  }
                : {'op': 'cancel', 'run_id': r['run_id']},
          );
        } catch (_) {}
      }
      throw const EngineException('执行已因应用生命周期变化而取消，请重新预览');
    }
    if (r['background'] != true) {
      originalResultRequest = cloneMap(request);
      resultRequest = request['protocol'] == 'modbus'
          ? null
          : cloneMap(request);
      resultOverrides = overrides;
      started(r, keepRequest: true);
    }
    return r;
  }

  void started(JsonMap response, {bool keepRequest = false}) {
    if (disposed || paused || response['background'] == true) return;
    resultRunId = response['run_id']?.toString() ?? '';
    if (!keepRequest) {
      originalResultRequest = null;
      resultRequest = null;
      resultOverrides = null;
    }
    events.clear();
    state['running'] = true;
    state['run_id'] = resultRunId;
    status = '执行中';
    notifyListeners();
  }

  Future<void> cancel() async {
    if (resultRunId.isEmpty) return;
    final id = resultRunId;
    status = '正在取消';
    notifyListeners();
    await command({'op': 'cancel', 'run_id': id});
  }

  Future<void> poll() async {
    if (polling || paused || disposed || !ready) return;
    polling = true;
    final epoch = _epoch;
    try {
      final batch = mapOf(await command({'op': 'events'}));
      if (disposed || paused || epoch != _epoch) return;
      bool changed = running != (batch['running'] == true);
      bool terminal = changed;
      for (final event in rowsOf(batch['events'])) {
        _stream.add(event);
        if (event['run_id']?.toString() != resultRunId) continue;
        events.add(event);
        if (events.length > 500) {
          events.removeAt(0);
          dropped++;
        }
        if (event['kind'] == 'started' &&
            originalResultRequest?['protocol'] == 'modbus') {
          final src = mapOf(mapOf(event['data'])['source']);
          if (src.isNotEmpty) {
            resultRequest = cloneMap(originalResultRequest!);
            resultRequest!['endpoint'] = src['endpoint'];
            resultRequest!['action'] = src['action'];
            resultRequest!['params'] = {
              ...mapOf(resultRequest!['params']),
              'unit': src['unit'],
            };
          }
        }
        if (event['kind'] == 'done') {
          terminal = true;
          final d = mapOf(event['data']);
          status = switch (d['status']) {
            'cancelled' => '已取消',
            'failed' => '执行失败',
            _ => '已完成',
          };
          if (d['error'] != null) error = d['error'].toString();
        }
        changed = true;
      }
      state['running'] = batch['running'] == true;
      dropped += (batch['dropped'] as num? ?? 0).toInt();
      evicted += (batch['evicted_result_count'] as num? ?? 0).toInt();
      if (changed) {
        if (terminal ||
            DateTime.now().difference(_lastVisual).inMilliseconds >= 1000) {
          _visualTimer?.cancel();
          _visualTimer = null;
          _lastVisual = DateTime.now();
          notifyListeners();
        } else {
          _visualTimer ??= Timer(const Duration(seconds: 1), () {
            _visualTimer = null;
            if (!disposed && !paused) {
              _lastVisual = DateTime.now();
              notifyListeners();
            }
          });
        }
      }
    } catch (e) {
      if (!disposed && !paused && epoch == _epoch) {
        error = '$e';
        notifyListeners();
      }
    } finally {
      polling = false;
    }
  }

  Future<void> setOptions({bool? readOnly, bool? history}) async {
    state = mapOf(
      await command({
        'op': 'options.set',
        'options': {
          'read_only': readOnly ?? this.readOnly,
          'history': history ?? this.history,
        },
      }),
    );
    await platform.invoke('settings.save', {
      'readOnly': this.readOnly,
      'history': this.history,
    });
    if (!disposed) notifyListeners();
  }

  Future<void> setProfile(String p) async {
    state = mapOf(await command({'op': 'profile.set', 'profile': p}));
    notifyListeners();
  }

  Future<void> pause() async {
    _epoch++;
    final wasRunning = running;
    paused = true;
    await engine.pause();
    if (disposed) return;
    state['running'] = false;
    if (wasRunning) status = '已取消';
    notifyListeners();
  }

  Future<void> resume() async {
    final epoch = ++_epoch;
    await engine.resume();
    final next = mapOf(await command({'op': 'state'}));
    if (disposed || epoch != _epoch) return;
    paused = false;
    state = next;
    notifyListeners();
  }

  int get epoch => _epoch;
  Future<void> exportText(String name, String text) async {
    final args = <String, dynamic>{'name': name, 'text': text};
    if (transfer != null) {
      await transfer!('files.export', args);
    } else {
      await platform.invoke('files.export', args);
    }
  }

  Future<void> switchCollection(String path, JsonMap preview) async {
    final next = mapOf(
      await command({
        'op': 'config.switch',
        'path': path,
        'token': preview['token'],
        'confirmed': true,
      }),
    );
    _collectionDrafts[currentCollection] = {
      for (final e in drafts.entries) e.key: cloneMap(e.value),
    };
    if (source != savedSource)
      _collectionSources[currentCollection] = source;
    else
      _collectionSources.remove(currentCollection);
    _epoch++;
    state = next;
    drafts
      ..clear()
      ..addAll(_collectionDrafts[currentCollection] ?? {});
    await refreshSource();
    source = _collectionSources[currentCollection] ?? source;
    draft = {};
    originalRequestId = null;
    resultRunId = '';
    resultRequest = null;
    originalResultRequest = null;
    events.clear();
    status = '尚未执行';
    final root = preferences['root']?.toString() ?? '';
    var relative = currentCollection;
    if (root.isNotEmpty && relative.startsWith('$root/'))
      relative = relative.substring(root.length + 1);
    await platform.invoke('settings.save', {'collection': relative});
    if (!disposed) notifyListeners();
  }

  void stateChanged(JsonMap value) {
    state = value;
    notifyListeners();
  }

  @override
  void dispose() {
    disposed = true;
    _timer?.cancel();
    _visualTimer?.cancel();
    _stream.close();
    engine.close();
    super.dispose();
  }
}
