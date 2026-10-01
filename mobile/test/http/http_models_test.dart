import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/http/crypto_editor.dart';
import 'package:iotools_mobile/features/http/http_tools.dart';
import 'package:iotools_mobile/features/history/history_page.dart';
import 'package:iotools_mobile/features/messaging/messaging_models.dart';

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
}
