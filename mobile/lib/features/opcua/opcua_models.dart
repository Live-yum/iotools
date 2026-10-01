import 'dart:collection';
import 'dart:convert';

/// Wire values deliberately retain decimal strings for exact Int64/UInt64 values.
typedef UaMap = Map<String, dynamic>;
UaMap uaMap(dynamic value) =>
    value is Map ? Map<String, dynamic>.from(value) : <String, dynamic>{};
UaMap uaCopy(UaMap value) => uaMap(jsonDecode(jsonEncode(value)));
String uaText(dynamic value) => value == null
    ? ''
    : value is String
    ? value
    : jsonEncode(value);
const uaTypes = <String>[
  'Boolean',
  'SByte',
  'Byte',
  'Int16',
  'UInt16',
  'Int32',
  'UInt32',
  'Int64',
  'UInt64',
  'Float',
  'Double',
  'String',
  'DateTime',
  'Guid',
  'NodeId',
  'ByteString',
  'LocalizedText',
  'QualifiedName',
];
const uaWritable = <String>[
  'Value',
  'DisplayName',
  'Description',
  'BrowseName',
  'Historizing',
  'Executable',
  'UserExecutable',
  'IsAbstract',
  'Symmetric',
  'ContainsNoLoops',
  'WriteMask',
  'UserWriteMask',
  'AccessLevelEx',
  'AccessLevel',
  'UserAccessLevel',
  'EventNotifier',
  'MinimumSamplingInterval',
  'ValueRank',
];
const uaConnectionFields = <String>[
  'security_policy',
  'security_mode',
  'allow_insecure',
  'allow_legacy_security',
  'auth',
  'username',
  'password',
  'cert_file',
  'key_file',
  'auth_cert_file',
  'auth_key_file',
  'server_cert_sha256',
  'ca_file',
  'application_uri',
];
String uaType(String value) =>
    value.replaceAll('TypeID', '').replaceAll('NodeID', 'NodeId');
String uaAttributeType(String name) {
  if (name == 'Value') return '';
  if (name == 'DisplayName' || name == 'Description') return 'LocalizedText';
  if (name == 'BrowseName') return 'QualifiedName';
  if (['WriteMask', 'UserWriteMask', 'AccessLevelEx'].contains(name))
    return 'UInt32';
  if (['AccessLevel', 'UserAccessLevel', 'EventNotifier'].contains(name))
    return 'Byte';
  if (name == 'MinimumSamplingInterval') return 'Double';
  if (name == 'ValueRank') return 'Int32';
  return 'Boolean';
}

int uaInteger(String text, int min, int max) {
  final n = int.tryParse(text);
  if (n == null || n < min || n > max)
    throw FormatException('请输入 $min 至 $max 的整数');
  return n;
}

void uaNodeId(String text) {
  if (text.length > 8192 ||
      !RegExp(
        r'^(?:ns=[0-9]{1,5};)?(?:i=[0-9]+|s=[\s\S]+|g=[0-9a-fA-F-]{36}|b=[A-Za-z0-9+/=]+)$',
      ).hasMatch(text))
    throw const FormatException('NodeId 格式应为 i=85 或 ns=2;s=名称');
  if (text.startsWith('ns='))
    uaInteger(text.substring(3, text.indexOf(';')), 0, 65535);
  final body = text.substring(text.indexOf(';') + 1);
  if (body.startsWith('i=') &&
      BigInt.parse(body.substring(2)) > BigInt.from(4294967295))
    throw const FormatException('数字 NodeId 超出 UInt32');
  if (body.startsWith('g=')) uaValidate('Guid', body.substring(2));
  if (body.startsWith('b=')) uaValidate('ByteString', body.substring(2));
}

dynamic uaValidate(String type, dynamic raw) {
  type = uaType(type);
  if (type.endsWith('[]')) {
    if (raw is! List || raw.length > 10000)
      throw const FormatException('一维数组最多 10000 项');
    return raw
        .map((v) => uaValidate(type.substring(0, type.length - 2), v))
        .toList();
  }
  if (!uaTypes.contains(type)) throw FormatException('不支持的类型：$type');
  if (raw == null) throw const FormatException('请输入值');
  final text = raw.toString();
  if (type == 'String') {
    if (raw is! String) throw const FormatException('需要文本');
    return raw;
  }
  if (type == 'Boolean') {
    if (raw is bool) return raw;
    if (text == 'true' || text == 'false') return text == 'true';
    throw const FormatException('Boolean 仅接受 true / false');
  }
  if (type == 'LocalizedText') {
    final v = uaMap(raw);
    if (v['text'] is! String || (v['locale'] != null && v['locale'] is! String))
      throw const FormatException('请输入文本与语言区域');
    return {'text': v['text'], 'locale': v['locale'] ?? ''};
  }
  if (type == 'QualifiedName') {
    final v = uaMap(raw);
    if (v['name'] is! String) throw const FormatException('请输入名称');
    return {
      'name': v['name'],
      'namespace': uaInteger('${v['namespace'] ?? 0}', 0, 65535),
    };
  }
  if (type == 'NodeId') {
    uaNodeId(text);
    return text;
  }
  if (type == 'Guid') {
    if (!RegExp(
      r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$',
    ).hasMatch(text))
      throw const FormatException('GUID 格式不正确');
    return text;
  }
  if (type == 'DateTime') {
    final match = RegExp(
      r'^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)(?:\.\d+)?(?:Z|([+-])(\d\d):(\d\d))$',
    ).firstMatch(text);
    if (match == null) throw const FormatException('时间需要 RFC3339 格式和时区');
    final year = int.parse(match[1]!);
    final month = int.parse(match[2]!);
    final day = int.parse(match[3]!);
    if (month < 1 ||
        month > 12 ||
        day < 1 ||
        day > DateTime.utc(year, month + 1, 0).day ||
        int.parse(match[4]!) > 23 ||
        int.parse(match[5]!) > 59 ||
        int.parse(match[6]!) > 59 ||
        (match[8] != null &&
            (int.parse(match[8]!) > 23 || int.parse(match[9]!) > 59)))
      throw const FormatException('时间字段超出有效范围');
    return text;
  }
  if (type == 'ByteString') {
    if (text.length % 4 != 0 ||
        !RegExp(r'^[A-Za-z0-9+/]*={0,2}$').hasMatch(text))
      throw const FormatException('需要标准 Base64 字节串');
    base64Decode(text);
    return text;
  }
  if (type == 'Float' || type == 'Double') {
    final n = double.tryParse(text);
    if (!RegExp(
          r'^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$',
        ).hasMatch(text) ||
        n == null ||
        !n.isFinite ||
        (type == 'Float' && n.abs() > 3.4028234663852886e38))
      throw const FormatException('需要有限且不溢出的十进制浮点数');
    return text;
  }
  if (!RegExp(r'^-?(0|[1-9][0-9]*)$').hasMatch(text))
    throw const FormatException('整数需要精确十进制文本');
  final bits = type == 'SByte' || type == 'Byte'
      ? 8
      : int.parse(type.replaceAll(RegExp(r'\D'), ''));
  final unsigned = type == 'Byte' || type.startsWith('U');
  final min = unsigned ? BigInt.zero : -(BigInt.one << (bits - 1));
  final max = (BigInt.one << (unsigned ? bits : bits - 1)) - BigInt.one;
  final n = BigInt.parse(text);
  if (n < min || n > max) throw FormatException('$type 范围为 $min 至 $max');
  return text;
}

/// Convert Go's display representation to editable typed fields without loss.
dynamic uaEditable(String type, dynamic value) {
  if (value == null) return uaDefault(type);
  if (type == 'Byte[]' && value is String)
    return base64Decode(value).map((v) => '$v').toList();
  if (type.endsWith('[]') && value is List)
    return value
        .map((v) => uaEditable(type.substring(0, type.length - 2), v))
        .toList();
  final map = uaMap(value);
  if (type == 'LocalizedText')
    return {
      'text': map['text'] ?? map['Text'] ?? '',
      'locale': map['locale'] ?? map['Locale'] ?? '',
    };
  if (type == 'QualifiedName')
    return {
      'name': map['name'] ?? map['Name'] ?? '',
      'namespace': map['namespace'] ?? map['NamespaceIndex'] ?? 0,
    };
  return value;
}

dynamic uaDefault(String type) {
  if (type.endsWith('[]')) return <dynamic>[];
  if (type == 'Boolean') return false;
  if (type == 'LocalizedText') return {'text': '', 'locale': ''};
  if (type == 'QualifiedName') return {'name': '', 'namespace': 0};
  if (type == 'NodeId') return 'i=85';
  if (type == 'Guid') return '00000000-0000-0000-0000-000000000000';
  if (type == 'DateTime') return DateTime.now().toUtc().toIso8601String();
  return ['String', 'ByteString'].contains(type) ? '' : '0';
}

UaMap uaOperation(UaMap base, String node, String action) {
  final source = uaMap(base['params']);
  return {
    'id': '${base['id'] ?? 'opcua'}-workspace',
    'name': 'OPC UA · $action',
    'protocol': 'opcua',
    'action': action,
    'endpoint': base['endpoint'],
    'timeout': base['timeout'] ?? '10s',
    'params': {
      for (final key in uaConnectionFields)
        if (source.containsKey(key)) key: source[key],
      if (action != 'discover') 'node_id': node,
      if (['browse', 'references', 'browse-path'].contains(action))
        'max_references': 1000,
    },
  };
}

String uaScope(UaMap request) {
  final p = uaMap(request['params']);
  // Private, memory-only key. It must never be logged, restored or displayed.
  return jsonEncode([
    request['endpoint'],
    for (final k in uaConnectionFields) p[k],
  ]);
}

String uaSafeEndpoint(dynamic raw) {
  final uri = Uri.tryParse('$raw');
  if (uri == null) return '无效端点';
  return uri
      .replace(
        userInfo: uri.userInfo.isEmpty ? '' : '已隐藏',
        query: uri.hasQuery ? '已隐藏' : null,
      )
      .toString();
}

class UaNode {
  UaNode(this.request, this.id) : key = '${uaScope(request)}|$id';
  final UaMap request;
  final String id, key;
  final browse = <UaMap>[], references = <UaMap>[], endpoints = <UaMap>[];
  final attributes = <String, UaMap>{};
  final largeResults = <UaMap>[];
  UaMap filter = {
    'direction': 'both',
    'include_subtypes': true,
    'max_references': 1000,
  };
  UaMap? method, methodResult, resolved;
  String path = '', status = '', resolvedEndpoint = '', resolvedNode = '';
  bool truncated = false;
  String get displayId => resolvedNode.isEmpty ? id : resolvedNode;
  void clear() {
    browse.clear();
    references.clear();
    endpoints.clear();
    attributes.clear();
    largeResults.clear();
    method = null;
    methodResult = null;
    resolved = null;
    path = '';
  }

  void begin(String action) {
    truncated = false;
    if (action == 'browse') browse.clear();
    if (action == 'references') references.clear();
    if (action == 'attributes') attributes.clear();
    if (action == 'discover') endpoints.clear();
    if (action == 'browse-path') resolved = null;
  }

  int get bytes =>
      jsonEncode([
        request,
        browse,
        references,
        endpoints,
        attributes,
        method,
        methodResult,
        resolved,
      ]).length *
      2;
  void add(List<UaMap> into, UaMap value) {
    if (into.length < 2000 &&
        bytes + jsonEncode(value).length * 2 <= 4 * 1024 * 1024) {
      into.add(uaCopy(value));
    } else {
      truncated = true;
    }
  }
}

class UaCache {
  final nodes = LinkedHashMap<String, UaNode>();
  final history = <String>[];
  int position = -1;
  UaNode node(UaMap request, String id) {
    final key = '${uaScope(request)}|$id';
    final value = nodes.remove(key) ?? UaNode(uaCopy(request), id);
    nodes[key] = value;
    trim();
    return value;
  }

  UaNode visit(UaMap request, String id) {
    final value = node(request, id);
    if (position < 0 || history[position] != value.key) {
      history.removeRange(position + 1, history.length);
      history.add(value.key);
      if (history.length > 256) history.removeAt(0);
      position = history.length - 1;
    }
    trim();
    return value;
  }

  UaNode get current => nodes[history[position]]!;
  void back() {
    if (position > 0) position--;
  }

  void forward() {
    if (position + 1 < history.length) position++;
  }

  void trim() {
    while (nodes.length > 32) {
      nodes.remove(
        nodes.keys.firstWhere((k) => position < 0 || k != history[position]),
      );
    }
    var bytes = nodes.values.fold<int>(0, (n, v) => n + v.bytes);
    while (bytes > 8 * 1024 * 1024 && nodes.length > 1) {
      final key = nodes.keys.firstWhere(
        (k) => position < 0 || k != history[position],
      );
      bytes -= nodes.remove(key)!.bytes;
    }
    for (var i = history.length - 1; i >= 0; i--) {
      if (!nodes.containsKey(history[i])) {
        history.removeAt(i);
        if (i <= position) position--;
      }
    }
    if (position < 0 && history.isNotEmpty) position = 0;
  }
}
