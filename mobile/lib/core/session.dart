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
  final List<JsonMap> events = [];
  JsonMap? originalResultRequest, resultRequest;
  JsonMap? resultOverrides;
  String resultRunId = '', status = '尚未执行', source = '', savedSource = '';
  String? error;
  bool ready = false, paused = false, polling = false, disposed = false;
  int dropped = 0, evicted = 0;
  Timer? _timer;
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
    draft = cloneMap(request);
    drafts[draft['id'].toString()] = cloneMap(draft);
    notifyListeners();
  }

  void updateDraft(JsonMap request) {
    draft = cloneMap(request);
    drafts[draft['id'].toString()] = cloneMap(draft);
  }

  JsonMap choose(JsonMap request) {
    draft = cloneMap(drafts[request['id']] ?? request);
    notifyListeners();
    return draft;
  }

  Future<void> saveRequest(JsonMap request) async {
    state = mapOf(await command({'op': 'request.save', 'request': request}));
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
    final r = mapOf(
      await command({
        'op': 'run',
        'token': preview['token'],
        'confirmed': confirmed,
      }),
    );
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
    if (response['background'] == true) return;
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
    await command({'op': 'cancel', 'run_id': resultRunId});
    status = '正在取消';
    notifyListeners();
  }

  Future<void> poll() async {
    if (polling || paused || disposed || !ready) return;
    polling = true;
    final epoch = _epoch;
    try {
      final batch = mapOf(await command({'op': 'events'}));
      if (disposed || paused || epoch != _epoch) return;
      bool changed = running != (batch['running'] == true);
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
      if (changed) notifyListeners();
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
    await platform.invoke('files.export', {'name': name, 'text': text});
  }

  void stateChanged(JsonMap value) {
    state = value;
    notifyListeners();
  }

  @override
  void dispose() {
    disposed = true;
    _timer?.cancel();
    _stream.close();
    engine.close();
    super.dispose();
  }
}
