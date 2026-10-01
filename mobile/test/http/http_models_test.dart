import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/http/crypto_editor.dart';
import 'package:iotools_mobile/features/http/http_tools.dart';
import 'package:iotools_mobile/features/history/history_page.dart';
import 'package:iotools_mobile/features/messaging/messaging_models.dart';
import 'package:iotools_mobile/features/messaging/kafka_entities.dart';
import 'package:iotools_mobile/features/messaging/messaging.dart';

void main() {
  test('codec fields are algorithm specific', () {
    expect(buildCodec('none'), {'algorithm': 'none'});
    expect(buildCodec('base64').containsKey('ciphertext_encoding'), isFalse);
    final c = buildCodec(
      'aes-128-cbc',
      key: '0123456789abcdef',
      iv: 'fedcba9876543210',
    );
    expect(c['ciphertext_encoding'], 'base64');
    expect(c['iv'], {'value': 'fedcba9876543210', 'encoding': 'text'});
    expect(buildCodec('aes-128-ecb').containsKey('iv'), isFalse);
  });
  test('history body is already text, including base64-looking text', () {
    expect(historyText({'body': 'test'}, 'body'), 'test');
    expect(historyText({'body': '中文😀'}, 'body'), '中文😀');
  });
  test('single run overrides preserve repeated query and deletion', () {
    expect(
      temporaryOverrides(fields: 'x=1', query: 'x=1\nx=2\ndelete')['query'],
      ['x=1', 'x=2', 'delete'],
    );
    expect(() => temporaryOverrides(fields: 'missing'), throwsFormatException);
  });
  test('MQTT exact empty levels and distinct numeric selectors', () {
    expect(topicPrefixes('/a'), ['', '/a']);
    expect(topicPrefixes('a//b'), ['a', 'a/', 'a//b']);
    expect(topicPrefixes('a/'), ['a', 'a/']);
    expect(topicUnder('a', ''), isFalse);
    expect(topicUnder('/a', ''), isTrue);
    final f = numericFields({
      'a.b': 1,
      'a': {'b': 2},
      'reading': '23 °C',
    });
    expect(f[r'$["a.b"]'], 1);
    expect(f[r'$["a"]["b"]'], 2);
    expect(f[r'$["reading"]'], 23);
    expect(f[r'$.length'], 3);
  });
  test('bounded topic deletion affects only exact topic', () {
    final c = TopicCache(perTopic: 2);
    for (var i = 0; i < 3; i++) {
      c.add({
        'run_id': 'r',
        'seq': i,
        'data': {'topic': 'a/'},
      });
    }
    c.add({
      'run_id': 'r',
      'seq': 4,
      'data': {'topic': 'a'},
    });
    expect(c.dropped, 1);
    c.clearTopic('a');
    expect(c.topics['a/']!.length, 2);
  });
  test(
    'MQTT boolean array and MessagePack map numeric values preserve semantics',
    () {
      expect(numericFields(true)[r'$'], 1);
      expect(numericFields([1, 2])[r'$'], 2);
      expect(numericFields({'a': 1})[r'$'], isNull);
      expect(numericFields({'a': 1}, messagepack: true)[r'$'], 1);
      expect(
        numericFields({'messagepack_type': 'binary'}, messagepack: true)[r'$'],
        isNull,
      );
      final events = [
        {
          'time': '2026-10-01T00:00:00Z',
          'data': {'retained': false},
        },
        {
          'time': '2026-10-01T00:00:01Z',
          'data': {'retained': true},
        },
        {
          'time': '2026-10-01T00:00:02Z',
          'data': {'retained': false},
        },
      ];
      expect(point(events, 2, 'rate'), .5);
    },
  );
  test('Kafka keyed maps and HTTP schema bodies become native entity rows', () {
    expect(
      kafkaEntities(
        'topics',
        {
          'orders': {'Topic': 'orders', 'Partitions': {}},
        },
        {'action': 'topics'},
      ).single['name'],
      'orders',
    );
    expect(
      kafkaEntities(
        'response',
        {
          'body': ['a', 'b'],
        },
        {'action': 'schemas'},
      ).map((e) => e['name']),
      ['a', 'b'],
    );
    final r = kafkaDraft(
      {
        'protocol': 'kafka',
        'params': {'body': 'stale', 'username': 'local'},
      },
      'lag',
      {'name': 'one-group'},
    );
    expect((r['params'] as Map)['groups'], ['one-group']);
    expect((r['params'] as Map).containsKey('body'), isFalse);
    expect((r['params'] as Map)['username'], 'local');
  });
}
