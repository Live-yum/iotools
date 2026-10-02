import 'dart:convert';

typedef JsonMap = Map<String, dynamic>;
JsonMap mapOf(Object? value) =>
    value is Map ? value.map((k, v) => MapEntry(k.toString(), v)) : {};
List<JsonMap> rowsOf(Object? value) =>
    value is List ? value.whereType<Map>().map(mapOf).toList() : [];
JsonMap cloneMap(JsonMap value) => mapOf(exactDecode(exactEncode(value)));
String pretty(Object? value) =>
    const JsonEncoder.withIndent('  ').convert(_printable(value));
Object? _printable(Object? v) => v is ExactNumber
    ? v.source
    : v is Map
    ? v.map((k, v) => MapEntry(k, _printable(v)))
    : v is List
    ? v.map(_printable).toList()
    : v;

/// Keeps raw JSON numeric tokens exact, including UInt64, without a double hop.
class ExactNumber {
  const ExactNumber(this.source);
  final String source;
  @override
  String toString() => source;
}

String exactEncode(Object? value) {
  if (value is ExactNumber) return value.source;
  if (value is BigInt) return value.toString();
  if (value is Map)
    return '{${value.entries.map((e) => '${jsonEncode(e.key.toString())}:${exactEncode(e.value)}').join(',')}}';
  if (value is List) return '[${value.map(exactEncode).join(',')}]';
  return jsonEncode(value);
}

Object? exactDecode(String source) => _Parser(source).parse();

/// Decode the engine wire without changing the JSON types of editable HTTP
/// bodies. Ordinary metrics remain doubles and display integers use the same
/// decimal strings as Go's wire encoder. Only params.json within a request
/// whose protocol is HTTP retains exact numeric tokens for later execution.
Object? decodeEngineReply(String source) {
  Object? normalized(Object? value, {bool preserveNumbers = false}) {
    if (value is ExactNumber) {
      if (preserveNumbers) return value;
      if (RegExp(r'^-?\d+$').hasMatch(value.source)) return value.source;
      final number = double.tryParse(value.source);
      if (number == null || !number.isFinite)
        throw const FormatException('内核返回非有限数值');
      return number;
    }
    if (value is List)
      return value
          .map((item) => normalized(item, preserveNumbers: preserveNumbers))
          .toList();
    if (value is Map)
      return {
        for (final entry in value.entries)
          entry.key.toString():
              !preserveNumbers &&
                  value['protocol'] == 'http' &&
                  entry.key == 'params' &&
                  entry.value is Map
              ? {
                  for (final parameter in (entry.value as Map).entries)
                    parameter.key.toString(): normalized(
                      parameter.value,
                      preserveNumbers: parameter.key == 'json',
                    ),
                }
              : normalized(entry.value, preserveNumbers: preserveNumbers),
      };
    return value;
  }

  return normalized(exactDecode(source));
}

class _Parser {
  _Parser(this.s);
  final String s;
  int p = 0;
  void ws() {
    while (p < s.length && ' \r\n\t'.contains(s[p])) {
      p++;
    }
  }

  Never bad() => throw FormatException('JSON 格式错误', s, p);
  Object? parse() {
    final v = value(0);
    ws();
    if (p != s.length) bad();
    return v;
  }

  Object? value(int depth) {
    if (depth > 128) bad();
    ws();
    if (p >= s.length) bad();
    final c = s[p];
    if (c == '"') {
      final start = p++;
      bool esc = false;
      while (p < s.length) {
        final x = s[p++];
        if (x == '"' && !esc) return jsonDecode(s.substring(start, p));
        if (x == '\\' && !esc) {
          esc = true;
        } else {
          esc = false;
        }
      }
      bad();
    }
    if (c == '{' || c == '[') {
      p++;
      final obj = <String, dynamic>{};
      final list = <dynamic>[];
      ws();
      final close = c == '{' ? '}' : ']';
      if (p < s.length && s[p] == close) {
        p++;
        return c == '{' ? obj : list;
      }
      while (true) {
        if (c == '{') {
          ws();
          if (p >= s.length || s[p] != '"') bad();
          final k = value(depth + 1) as String;
          if (obj.containsKey(k)) throw FormatException('JSON 键重复：$k');
          ws();
          if (p >= s.length || s[p++] != ':') bad();
          obj[k] = value(depth + 1);
        } else {
          list.add(value(depth + 1));
        }
        ws();
        if (p >= s.length) bad();
        if (s[p] == close) {
          p++;
          return c == '{' ? obj : list;
        }
        if (s[p++] != ',') bad();
      }
    }
    for (final pair in {'true': true, 'false': false, 'null': null}.entries) {
      if (s.startsWith(pair.key, p)) {
        p += pair.key.length;
        return pair.value;
      }
    }
    final m = RegExp(
      r'-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?',
    ).matchAsPrefix(s, p);
    if (m == null) bad();
    final n = m.group(0)!;
    p = m.end;
    if (!n.contains(RegExp(r'[.eE]'))) {
      final integer = BigInt.parse(n);
      if (integer.abs() <= BigInt.from(9007199254740991))
        return integer.toInt();
    }
    return ExactNumber(n);
  }
}

final _secretEndpointKey = RegExp(
  r'token|password|passwd|secret|api.?key|authorization|credential',
  caseSensitive: false,
);

/// Display only: resolve ordinary profile variables, never environment or
/// executable templates. Preserve unknown placeholders rather than URI-encode.
String displayEndpoint(String input, JsonMap variables) {
  final resolved = input.replaceAllMapped(RegExp(r'\$\{([^}]+)\}'), (m) {
    final key = m.group(1)!;
    if (_secretEndpointKey.hasMatch(key)) return '••••';
    if (key.startsWith('env:')) return m.group(0)!;
    return variables[key]?.toString() ?? m.group(0)!;
  });
  final safe = redactedEndpoint(resolved);
  return resolved.contains(RegExp(r'\$\{|\{\{')) ? '$safe（待解析变量）' : safe;
}

String redactedEndpoint(String input) {
  // Work on the original text. Uri.toString would encode template braces and
  // can introduce empty query/fragment markers on non-HTTP protocol addresses.
  var result = input.replaceFirstMapped(
    RegExp(r'^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/?#]*@'),
    (m) => '${m.group(1)}••••@',
  );
  result = result.replaceAllMapped(RegExp(r'([?&])([^=&#]+)=([^&#]*)'), (m) {
    var key = m.group(2)!;
    try {
      key = Uri.decodeQueryComponent(key);
    } catch (_) {}
    return _secretEndpointKey.hasMatch(key)
        ? '${m.group(1)}${m.group(2)}=••••'
        : m.group(0)!;
  });
  return result;
}
