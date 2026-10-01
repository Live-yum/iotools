import 'dart:convert';
import 'dart:async';
import 'package:flutter/foundation.dart';
import 'modbus_host.dart';
import 'modbus_models.dart';

class ModbusController extends ChangeNotifier {
  ModbusController(this.host) {
    sync();
    host.addListener(sync);
    _subscription = host.eventStream.listen((event) {
      cache.add(event);
      notifyListeners();
    });
  }
  final ModbusHost host;
  StreamSubscription<Map<String, dynamic>>? _subscription;
  final cache = ModbusCache();
  Map<String, dynamic> draft = {};
  final Map<String, Map<String, dynamic>> _drafts = {};
  final Set<String> _dirty = {};
  bool dirty = false, pinnedOnly = false, matrix = false, frozen = false;
  List<ModbusFrame>? frozenFrames;
  int selectedAddress = 0;
  String selectedField = 'u16';
  Map<String, dynamic> get params => mbMap(draft['params']);
  void sync() {
    final actual = host.resultRequest;
    if (actual != null && host.resultRunId.isNotEmpty) {
      cache.add({
        'kind': 'started',
        'run_id': host.resultRunId,
        'data': {
          'source': {
            'request_id': actual['id'],
            'endpoint': actual['endpoint'],
            'action': actual['action'],
            'unit': mbMap(actual['params'])['unit'],
          },
        },
      });
    }
    for (final event in host.events) {
      cache.add(event);
    }
    final current = host.request;
    if (current['protocol'] == 'modbus') {
      final id = current['id'].toString();
      if (draft['id'] != id) {
        if (draft.isNotEmpty) {
          _drafts[draft['id'].toString()] = mbClone(draft);
        }
        draft = mbClone(_drafts[id] ?? current);
        dirty = _dirty.contains(id);
        selectedAddress = int.tryParse(params['address'].toString()) ?? 0;
      } else if (!dirty) {
        draft = mbClone(current);
      }
    }
    notifyListeners();
  }

  void edit(Map<String, dynamic> changed) {
    draft = mbClone(changed);
    dirty = true;
    _dirty.add(draft['id'].toString());
    _drafts[draft['id'].toString()] = mbClone(draft);
    notifyListeners();
  }

  void editParams(Map<String, dynamic> changed) {
    final next = mbClone(draft);
    next['params'] = mbClone(changed);
    edit(next);
  }

  String scopeFor(Map<String, dynamic> request) {
    try {
      final p = mbMap(request['params']);
      final endpoint = request['endpoint'].toString(),
          unit = (p['unit'] ?? 1).toString();
      final space = registerSpace(request['action'].toString());
      // Templates map only through an immutable original execution request.
      for (final frame in cache.frames.reversed) {
        final original = host.originalRequest(frame.source.runId);
        if (original != null &&
            original['id'] == request['id'] &&
            original['endpoint'] == request['endpoint'] &&
            (mbMap(original['params'])['unit'] ?? 1).toString() == unit &&
            registerSpace(original['action'].toString()) == space) {
          return frame.source.scope;
        }
      }
      return '$endpoint\n$unit\n$space';
    } catch (_) {
      return '';
    }
  }

  List<ModbusFrame> get frames => frozen
      ? (frozenFrames ?? [])
            .where((frame) => frame.source.scope == scopeFor(draft))
            .toList()
      : cache.forScope(scopeFor(draft));
  ModbusFrame? get current => frames.isEmpty ? null : frames.last;
  void freeze(bool value) {
    if (value) frozenFrames = List.of(frames);
    frozen = value;
    notifyListeners();
  }

  void clearCache() {
    cache.clear();
    frozenFrames = null;
    notifyListeners();
  }

  Map<String, dynamic> get columns {
    final value = mbMap(params['columns']);
    try {
      validateColumns(value);
      return value;
    } catch (_) {
      return {
        'visible': ['address', 'u16', 'i16', 'hex', 'custom', 'label'],
        'address_mode': 'decimal',
        'time_mode': 'read_at',
        'widths': <String, dynamic>{},
      };
    }
  }

  String formatAddress(int address) => columns['address_mode'] == 'hex'
      ? '0x${address.toRadixString(16).toUpperCase().padLeft(4, '0')}'
      : '$address';
  String display(ModbusFrame frame, Map<String, dynamic> row, String field) {
    if (field == 'address')
      return formatAddress(boundedInt(row['address'], 0, 65535, '地址'));
    if (field == 'label')
      return (mbMap(params['labels'])[row['address'].toString()] ??
              row['label'] ??
              '')
          .toString();
    if (field == 'time') {
      if (columns['time_mode'] == 'ago') {
        final time = DateTime.tryParse(frame.time);
        if (time != null)
          return '${DateTime.now().difference(time).inSeconds.clamp(0, 1 << 31)} 秒前';
      }
      return frame.time;
    }
    return row[field]?.toString() ?? '—';
  }

  List<Map<String, dynamic>> visibleRows(ModbusFrame frame) {
    final start = int.tryParse(params['address'].toString()) ?? 0;
    final count = int.tryParse(params['count'].toString()) ?? 1;
    final pins = (params['pins'] as List? ?? [])
        .map((value) => value.toString())
        .toSet();
    return frame.rows.where((row) {
      final address = boundedInt(row['address'], 0, 65535, '地址');
      return address >= start &&
          address < start + count &&
          (!pinnedOnly || pins.contains('$address'));
    }).toList();
  }

  Future<void> navigate({
    required String address,
    required int count,
    required int unit,
    required String action,
    required String order,
  }) async {
    final p = mbClone(params);
    final maximum = ['coil', 'discrete'].contains(registerSpace(action))
        ? 2000
        : 125;
    boundedInt(count, 1, maximum, '数量');
    boundedInt(unit, 1, 247, '单元');
    if (!modbusWordOrders.contains(order))
      throw const FormatException('未知字节顺序');
    final target = parseModbusAddress(
      address,
      int.tryParse(p['address'].toString()) ?? 0,
      count,
      mbMap(p['labels']),
    );
    if (registerSpace(draft['action'].toString()) != registerSpace(action)) {
      p.remove('pins');
      p.remove('labels');
      p.remove('rules');
    }
    p.addAll({
      'address': target,
      'count': count,
      'unit': unit,
      'word_order': order,
    });
    final next = mbClone(draft)..['action'] = action;
    next['params'] = p;
    edit(next);
    selectedAddress = target;
    await reinterpret();
  }

  Future<void> reinterpret() async {
    final snapshot = mbClone(draft), revision = jsonEncode(draft);
    final matching = List<ModbusFrame>.of(frames);
    for (final frame in matching) {
      if (frame.words.isEmpty ||
          frame.gap ||
          scopeFor(snapshot) != frame.source.scope)
        continue;
      final rows = mbRows(
        await host.command({
          'op': 'modbus.interpret',
          'request': snapshot,
          'words': frame.words,
        }),
      );
      if (jsonEncode(draft) != revision) return;
      final index = cache.frames.indexOf(frame);
      if (index >= 0)
        cache.frames[index] = ModbusFrame(
          source: frame.source,
          time: frame.time,
          sequence: frame.sequence,
          words: frame.words,
          rows: rows,
        );
    }
    notifyListeners();
  }

  Future<void> save() async {
    if (host.readOnly) throw const FormatException('只读模式禁止保存配置');
    validateColumns(columns);
    final next = mbClone(draft);
    await host.command({'op': 'modbus.rules', 'request': next});
    await host.saveRequest(next);
    if (jsonEncode(draft) == jsonEncode(next)) {
      dirty = false;
      _dirty.remove(next['id']);
    }
    notifyListeners();
  }

  String csv() {
    final frame = current;
    if (frame == null || frame.gap) throw const FormatException('没有匹配的完整响应');
    final visible = (columns['visible'] as List).cast<String>();
    final output = StringBuffer(
      '${['type', 'captured_utc', ...visible].map(csvCell).join(',')}\n',
    );
    for (final row in visibleRows(frame)) {
      output.writeln(
        [
          frame.source.space,
          frame.time,
          ...visible.map((field) => display(frame, row, field)),
        ].map(csvCell).join(','),
      );
    }
    return output.toString();
  }

  @override
  void dispose() {
    host.removeListener(sync);
    _subscription?.cancel();
    super.dispose();
  }
}
