import 'dart:collection';
import 'dart:convert';

Map<String, dynamic> mbMap(Object? value) => value is Map
    ? value.map((key, value) => MapEntry(key.toString(), value))
    : <String, dynamic>{};
Map<String, dynamic> mbClone(Map<String, dynamic> value) =>
    mbMap(jsonDecode(jsonEncode(value)));
List<Map<String, dynamic>> mbRows(Object? value) => value is List
    ? value.whereType<Map>().map(mbMap).toList()
    : <Map<String, dynamic>>[];

const modbusColumns = <String>[
  'address',
  'time',
  'u16',
  'i16',
  'u8',
  'i8',
  'hex',
  'hex32',
  'f16',
  'bcd',
  'bcd32',
  'u32',
  'i32',
  'u32_m10k',
  'i32_m10k',
  'u64',
  'i64',
  'f32',
  'f64',
  'ascii',
  'binary',
  'custom',
  'label',
];
const modbusReadActions = <String>[
  'read-holding',
  'read-input',
  'read-coils',
  'read-discrete',
];
const modbusWordOrders = <String>['ABCD', 'BADC', 'CDAB', 'DCBA'];
const modbusValueTypes = <String>[
  'u16',
  'i16',
  'f16',
  'u32',
  'i32',
  'f32',
  'u64',
  'i64',
  'f64',
];

String registerSpace(String action) {
  switch (action) {
    case 'read-holding':
    case 'sweep-holding':
    case 'search-holding':
    case 'write-register':
    case 'write-registers':
    case 'write-typed':
    case 'read-write-registers':
      return 'holding';
    case 'read-input':
    case 'sweep-input':
      return 'input';
    case 'read-coils':
    case 'sweep-coils':
    case 'write-coil':
    case 'write-coils':
      return 'coil';
    case 'read-discrete':
    case 'sweep-discrete':
      return 'discrete';
    default:
      throw const FormatException('请选择明确的 Modbus 寄存器空间');
  }
}

String readAction(String action) => const {
  'holding': 'read-holding',
  'input': 'read-input',
  'coil': 'read-coils',
  'discrete': 'read-discrete',
}[registerSpace(action)]!;

int boundedInt(Object? value, int minimum, int maximum, String name) {
  final raw = value?.toString().trim() ?? '';
  final number = RegExp(r'^\d+$').hasMatch(raw) ? BigInt.tryParse(raw) : null;
  if (number == null ||
      number < BigInt.from(minimum) ||
      number > BigInt.from(maximum)) {
    throw FormatException('$name 必须是 $minimum–$maximum 的整数');
  }
  return number.toInt();
}

/// Exact integer validation never passes through double (including uint64 max).
String exactTypedValue(String type, String input) {
  final raw = input.trim();
  final bounds = <String, List<BigInt>>{
    'u16': [BigInt.zero, (BigInt.one << 16) - BigInt.one],
    'i16': [-(BigInt.one << 15), (BigInt.one << 15) - BigInt.one],
    'u32': [BigInt.zero, (BigInt.one << 32) - BigInt.one],
    'i32': [-(BigInt.one << 31), (BigInt.one << 31) - BigInt.one],
    'u64': [BigInt.zero, (BigInt.one << 64) - BigInt.one],
    'i64': [-(BigInt.one << 63), (BigInt.one << 63) - BigInt.one],
  };
  if (bounds.containsKey(type)) {
    final value = RegExp(r'^[+-]?\d+$').hasMatch(raw)
        ? BigInt.tryParse(raw)
        : null;
    if (value == null || value < bounds[type]![0] || value > bounds[type]![1]) {
      throw FormatException('$type 整数越界或格式错误');
    }
    return value.toString();
  }
  if (!const ['f16', 'f32', 'f64'].contains(type)) {
    throw const FormatException('不支持的写入类型');
  }
  final number = double.tryParse(raw);
  if (number == null || !number.isFinite) {
    throw const FormatException('浮点数必须有限');
  }
  // Encoding and representability are checked by the same Go encoder as writes.
  return raw;
}

int parseModbusAddress(
  String raw,
  int current,
  int count,
  Map<String, dynamic> labels,
) {
  final text = raw.trim();
  if (text.isEmpty) throw const FormatException('请输入地址或唯一标签');
  BigInt? target;
  final numeric = RegExp(
    r'^([+-]?)(0[xX][0-9a-fA-F]+|[0-9]+)$',
  ).firstMatch(text);
  if (numeric != null) {
    final digits = numeric.group(2)!;
    final value = digits.toLowerCase().startsWith('0x')
        ? BigInt.parse(digits.substring(2), radix: 16)
        : BigInt.parse(digits);
    target = numeric.group(1)!.isEmpty
        ? value
        : BigInt.from(current) + (numeric.group(1) == '-' ? -value : value);
  } else {
    final matches = labels.entries
        .where((entry) => entry.value == text)
        .toList();
    if (matches.length > 1) throw const FormatException('标签不唯一，请使用明确地址');
    if (matches.isEmpty) throw const FormatException('地址格式错误或没有匹配标签');
    target = BigInt.from(boundedInt(matches.single.key, 0, 65535, '标签地址'));
  }
  if (count < 1 ||
      count > 2000 ||
      target < BigInt.zero ||
      target + BigInt.from(count) > BigInt.from(65536)) {
    throw const FormatException('地址窗口必须位于 0–65535，不能越界');
  }
  return target.toInt();
}

void validateColumns(Map<String, dynamic> columns) {
  if (columns.keys.any(
    (key) =>
        !const ['visible', 'widths', 'address_mode', 'time_mode'].contains(key),
  )) {
    throw const FormatException('未知列布局选项');
  }
  final visible = columns['visible'];
  if (visible is! List ||
      visible.isEmpty ||
      visible.length > modbusColumns.length ||
      visible.any((key) => !modbusColumns.contains(key)) ||
      visible.toSet().length != visible.length) {
    throw const FormatException('显示列不能为空、重复或未知');
  }
  for (final entry in mbMap(columns['widths']).entries) {
    if (!modbusColumns.contains(entry.key))
      throw const FormatException('未知宽度列');
    boundedInt(entry.value, 1, 120, '列宽');
  }
  if (!const [
        'decimal',
        'hex',
      ].contains(columns['address_mode'] ?? 'decimal') ||
      !const ['read_at', 'ago'].contains(columns['time_mode'] ?? 'read_at')) {
    throw const FormatException('未知地址或时间显示方式');
  }
}

List<Map<String, dynamic>> replaceRule(
  List<Map<String, dynamic>> original,
  int address,
  Map<String, dynamic>? replacement,
) => [
  for (final rule in original)
    if (boundedInt(rule['address'], 0, 65535, '规则地址') != address) mbClone(rule),
  if (replacement != null) mbClone(replacement),
];

Map<int, dynamic> _addressMap(Object? source, String name) {
  final result = <int, dynamic>{};
  for (final entry in mbMap(source).entries) {
    final address = boundedInt(entry.key, 0, 65535, name);
    if (result.containsKey(address)) throw FormatException('$name 地址重复');
    result[address] = entry.value;
  }
  return result;
}

Map<int, Map<String, dynamic>> ruleMap(Object? source) {
  final result = <int, Map<String, dynamic>>{};
  for (final rule in mbRows(source)) {
    final address = boundedInt(rule['address'], 0, 65535, '规则');
    if (result.containsKey(address)) throw const FormatException('规则地址重复');
    result[address] = mbClone(rule);
  }
  return result;
}

/// Explicit exact-space merge. Runtime, connection and other spaces never enter.
Map<String, dynamic> mergeAnnotations(
  Map<String, dynamic> request,
  String incomingAction,
  Map<String, dynamic> incoming, {
  Set<int> overwriteLabels = const {},
  Set<int> overwriteRules = const {},
}) {
  if (registerSpace(request['action'].toString()) !=
      registerSpace(incomingAction)) {
    throw const FormatException('导入空间与当前请求不同');
  }
  final result = mbClone(request), params = mbClone(mbMap(request['params']));
  final pins = <int>{};
  for (final value in [
    ...(params['pins'] as List? ?? []),
    ...(incoming['pins'] as List? ?? []),
  ]) {
    pins.add(boundedInt(value, 0, 65535, '固定地址'));
  }
  final labels = _addressMap(params['labels'], '标签');
  for (final entry in _addressMap(incoming['labels'], '标签').entries) {
    if (!labels.containsKey(entry.key) || overwriteLabels.contains(entry.key))
      labels[entry.key] = entry.value;
  }
  final rules = ruleMap(params['rules']);
  for (final entry in ruleMap(incoming['rules']).entries) {
    if (!rules.containsKey(entry.key) || overwriteRules.contains(entry.key))
      rules[entry.key] = entry.value;
  }
  params['pins'] = pins.toList()..sort();
  params['labels'] = {
    for (final key in (labels.keys.toList()..sort())) '$key': labels[key],
  };
  params['rules'] = [
    for (final key in (rules.keys.toList()..sort())) rules[key],
  ];
  result['params'] = params;
  return result;
}

String csvCell(Object? value) {
  var text = value?.toString() ?? '';
  if (text.isNotEmpty &&
      '=+-@\t\r'.contains(text[0]) &&
      ExactDecimal.tryParse(text) == null)
    text = "'$text";
  return '"${text.replaceAll('"', '""')}"';
}

/// Decimal comparison/sum without truncating integers above 2^53 or uint64.
class ExactDecimal implements Comparable<ExactDecimal> {
  const ExactDecimal(this.coefficient, this.scale);
  final BigInt coefficient;
  final int scale;
  static ExactDecimal? tryParse(Object? value) {
    final raw = value?.toString() ?? '';
    if (raw.length > 512) return null;
    final match = RegExp(
      r'^([+-]?)(\d+)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$',
    ).firstMatch(raw);
    if (match == null) return null;
    final exponent = int.tryParse(match.group(4) ?? '0');
    if (exponent == null || exponent.abs() > 308) return null;
    var coefficient = BigInt.parse('${match.group(2)}${match.group(3) ?? ''}');
    if (match.group(1) == '-') coefficient = -coefficient;
    var scale = (match.group(3)?.length ?? 0) - exponent;
    if (scale < 0) {
      coefficient *= BigInt.from(10).pow(-scale);
      scale = 0;
    }
    return ExactDecimal(coefficient, scale);
  }

  BigInt _aligned(int target) =>
      coefficient * BigInt.from(10).pow(target - scale);
  @override
  int compareTo(ExactDecimal other) {
    final target = scale > other.scale ? scale : other.scale;
    return _aligned(target).compareTo(other._aligned(target));
  }

  ExactDecimal operator +(ExactDecimal other) {
    final target = scale > other.scale ? scale : other.scale;
    return ExactDecimal(_aligned(target) + other._aligned(target), target);
  }

  ExactDecimal operator -(ExactDecimal other) =>
      this + ExactDecimal(-other.coefficient, other.scale);
  String average(int count) => ExactDecimal(
    coefficient * BigInt.from(10).pow(16) ~/ BigInt.from(count),
    scale + 16,
  ).toString();
  double toDouble() => double.parse(toString());
  @override
  String toString() {
    final negative = coefficient.isNegative;
    var digits = coefficient.abs().toString().padLeft(scale + 1, '0');
    if (scale > 0) {
      digits =
          '${digits.substring(0, digits.length - scale)}.${digits.substring(digits.length - scale)}';
      digits = digits
          .replaceFirst(RegExp(r'0+$'), '')
          .replaceFirst(RegExp(r'\.$'), '');
    }
    return '${negative ? '-' : ''}$digits';
  }
}

class ModbusSource {
  ModbusSource({
    required this.runId,
    required this.requestId,
    required this.endpoint,
    required this.unit,
    required this.action,
  });
  final String runId, requestId, endpoint, action;
  final int unit;
  String get space => registerSpace(action);
  String get scope => '$endpoint\n$unit\n$space';
  static ModbusSource? fromStarted(Map<String, dynamic> event) {
    if (event['kind'] != 'started') return null;
    final source = mbMap(mbMap(event['data'])['source']);
    try {
      final run = event['run_id']?.toString() ?? '';
      final endpoint = source['endpoint']?.toString() ?? '';
      if (run.isEmpty || endpoint.isEmpty) return null;
      registerSpace(source['action'].toString());
      return ModbusSource(
        runId: run,
        requestId: source['request_id']?.toString() ?? '',
        endpoint: endpoint,
        unit: boundedInt(source['unit'], 1, 247, '来源单元'),
        action: source['action'].toString(),
      );
    } catch (_) {
      return null;
    }
  }
}

class ModbusFrame {
  ModbusFrame({
    required this.source,
    required this.time,
    required this.sequence,
    required Map<String, dynamic> words,
    required List<Map<String, dynamic>> rows,
    this.gap = false,
  }) : words = Map.unmodifiable(mbClone(words)),
       rows = List.unmodifiable(
         rows.map((row) => Map<String, dynamic>.unmodifiable(mbClone(row))),
       );
  final ModbusSource source;
  final String time, sequence;
  final Map<String, dynamic> words;
  final List<Map<String, dynamic>> rows;
  final bool gap;
  Map<String, dynamic> snapshot() {
    if (gap || words.isEmpty || !['holding', 'input'].contains(source.space)) {
      throw const FormatException('快照需要有真实来源的完整寄存器响应');
    }
    return {
      'version': 1,
      'endpoint': source.endpoint,
      'unit': source.unit,
      'action': readAction(source.action),
      'time': time,
      'values': mbClone(words),
    };
  }
}

/// Each frame is exactly one response. Missing operands cannot cross a frame.
class ModbusCache {
  final _sources = LinkedHashMap<String, ModbusSource>();
  final _seen = LinkedHashSet<String>();
  final List<ModbusFrame> frames = [];
  int evicted = 0, _cells = 0;
  void clear() {
    frames.clear();
    _cells = 0;
    evicted = 0;
  }

  void add(Map<String, dynamic> event) {
    final source = ModbusSource.fromStarted(event);
    if (source != null) {
      _sources[source.runId] = source;
      while (_sources.length > 64) {
        _sources.remove(_sources.keys.first);
      }
      return;
    }
    final run = event['run_id']?.toString() ?? '';
    final actual = _sources[run];
    if (actual == null)
      return; // Never relabel old data using the current draft.
    final key = '$run:${event['seq']}';
    if (_seen.contains(key)) return;
    final kind = event['kind'];
    final data = mbMap(event['data']);
    final failure =
        ['error', 'result-error', 'sweep-error'].contains(kind) ||
        (kind == 'done' && data['status'] == 'failed') ||
        data['truncated'] == true;
    if (!['registers', 'bits'].contains(kind) && !failure) return;
    _seen.add(key);
    while (_seen.length > 1024) {
      _seen.remove(_seen.first);
    }
    var rows = <Map<String, dynamic>>[];
    final words = <String, dynamic>{};
    try {
      if (!failure && kind == 'bits') {
        final values = data['values'];
        if (values is! List || values.length > 2000) return;
        final start = boundedInt(data['address'], 0, 65535, '地址');
        for (var i = 0; i < values.length; i++) {
          if (values[i] is! bool || start + i > 65535) return;
          rows.add({
            'address': start + i,
            'u16': values[i] ? 1 : 0,
            'value': values[i],
            'binary': values[i] ? '1' : '0',
          });
        }
      } else if (!failure) {
        if (event['data'] is! List || (event['data'] as List).length > 2000)
          return;
        rows = mbRows(event['data']);
      }
      for (final row in rows) {
        final address = boundedInt(row['address'], 0, 65535, '地址');
        if (words.containsKey('$address')) return;
        words['$address'] = boundedInt(row['u16'], 0, 65535, '原始字');
      }
    } catch (_) {
      return;
    }
    final frame = ModbusFrame(
      source: actual,
      time: event['time']?.toString() ?? '',
      sequence: key,
      words: words,
      rows: rows,
      gap: failure,
    );
    frames.add(frame);
    _cells += rows.length;
    while (frames.length > 64 || _cells > 8192) {
      _cells -= frames.removeAt(0).rows.length;
      evicted++;
    }
  }

  List<ModbusFrame> forScope(String scope) =>
      frames.where((frame) => frame.source.scope == scope).toList();
}

class ModbusTrend {
  ModbusTrend(List<ModbusFrame> frames, int address, String field) {
    for (final frame in frames) {
      ExactDecimal? number;
      for (final row in frame.rows) {
        if (row['address'].toString() == '$address') {
          number = ExactDecimal.tryParse(row[field]);
          break;
        }
      }
      points.add(number);
      if (number == null) continue;
      minimum = minimum == null || number.compareTo(minimum!) < 0
          ? number
          : minimum;
      maximum = maximum == null || number.compareTo(maximum!) > 0
          ? number
          : maximum;
      sum = sum + number;
      valid++;
    }
  }
  final points = <ExactDecimal?>[];
  ExactDecimal? minimum, maximum;
  ExactDecimal sum = ExactDecimal(BigInt.zero, 0);
  int valid = 0;
  String get now => points.isEmpty || points.last == null
      ? '缺失 / 无效值'
      : points.last.toString();
  String get average => valid == 0 ? '—' : sum.average(valid);
}
