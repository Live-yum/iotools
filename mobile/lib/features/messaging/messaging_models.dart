import 'dart:collection';
import 'dart:convert';
import '../../core/json.dart';

List<String> topicPrefixes(String topic) {
  final result = <String>[];
  for (var i = 0; i < topic.length; i++) {
    if (topic[i] == '/') result.add(topic.substring(0, i));
  }
  result.add(topic);
  return result;
}

bool topicUnder(String topic, String prefix) =>
    topic == prefix || topic.startsWith('$prefix/');

class TopicCache {
  TopicCache({
    this.maxTopics = 200,
    this.perTopic = 100,
    this.maxMessages = 1000,
    this.maxBytes = 8 * 1024 * 1024,
  });
  final int maxTopics, perTopic, maxMessages, maxBytes;
  final LinkedHashMap<String, List<JsonMap>> topics = LinkedHashMap();
  int dropped = 0, bytes = 0;
  final Set<String> _seen = {};
  void add(JsonMap event) {
    final id = '${event['run_id']}/${event['seq']}';
    if (!_seen.add(id)) return;
    if (_seen.length > 4000) _seen.remove(_seen.first);
    final d = mapOf(event['data']);
    final topic = d['topic']?.toString();
    if (topic == null) return;
    final size = utf8.encode(exactEncode(event)).length;
    if (size > maxBytes) {
      dropped++;
      return;
    }
    if (!topics.containsKey(topic) && topics.length >= maxTopics) {
      final oldest = topics.keys.first;
      for (final e in topics.remove(oldest)!) {
        bytes -= utf8.encode(exactEncode(e)).length;
        dropped++;
      }
    }
    final list = topics.putIfAbsent(topic, () => []);
    list.add(cloneMap(event));
    bytes += size;
    while (list.length > perTopic) {
      bytes -= utf8.encode(exactEncode(list.removeAt(0))).length;
      dropped++;
    }
    while (count > maxMessages || bytes > maxBytes) {
      final candidates =
          topics.entries.where((e) => e.value.isNotEmpty).toList()..sort(
            (a, b) => a.value.first['time'].toString().compareTo(
              b.value.first['time'].toString(),
            ),
          );
      if (candidates.isEmpty) break;
      final e = candidates.first.value.removeAt(0);
      bytes -= utf8.encode(exactEncode(e)).length;
      dropped++;
    }
  }

  int get count => topics.values.fold(0, (n, l) => n + l.length);
  void deleteMessage(String topic, Object? seq) {
    final list = topics[topic];
    if (list == null) return;
    list.removeWhere((e) {
      if (e['seq'] == seq) {
        bytes -= utf8.encode(exactEncode(e)).length;
        return true;
      }
      return false;
    });
  }

  void clearTopic(String topic) {
    for (final e in topics.remove(topic) ?? <JsonMap>[]) {
      bytes -= utf8.encode(exactEncode(e)).length;
    }
  }

  void clear() {
    topics.clear();
    bytes = 0;
  }
}

Map<String, num> numericFields(Object? input, {bool messagepack = false}) {
  final result = <String, num>{};
  void walk(Object? value, String path, int depth) {
    if (depth > 16 || result.length > 200) return;
    if (value is bool) {
      result[path] = value ? 1 : 0;
      return;
    }
    if (value is num && value.isFinite) {
      result[path] = value;
      return;
    }
    if (value is ExactNumber) {
      final n = num.tryParse(value.source);
      if (n != null && n.isFinite) result[path] = n;
      return;
    }
    if (value is String) {
      final first = value.trim().split(RegExp(r'\s+')).first;
      final n = num.tryParse(first);
      if (n != null && n.isFinite) result[path] = n;
      return;
    }
    if (value is List) {
      result[path] = value.length;
      result['$path.length'] = value.length;
      for (var i = 0; i < value.length && i < 64; i++) {
        walk(value[i], '$path[$i]', depth + 1);
      }
      return;
    }
    if (value is Map) {
      if (!value.containsKey('messagepack_type')) {
        result['$path.length'] = value.length;
        if (messagepack) result[path] = value.length;
      }
      for (final e in value.entries) {
        walk(e.value, '$path[${jsonEncode(e.key.toString())}]', depth + 1);
      }
    }
  }

  walk(input, r'$', 0);
  return result;
}

String kafkaOrderKey(JsonMap event, String field) {
  final d = mapOf(event['data']);
  return d[field]?.toString() ?? '';
}

int compareExact(String a, String b) {
  final ai = BigInt.tryParse(a), bi = BigInt.tryParse(b);
  return ai != null && bi != null ? ai.compareTo(bi) : a.compareTo(b);
}
