import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/modbus/modbus_models.dart';

Map<String, dynamic> request() => {
  'id': 'meter',
  'protocol': 'modbus',
  'endpoint': 'tcp://example:502',
  'action': 'read-holding',
  'params': {
    'unit': 1,
    'address': 1,
    'count': 2,
    'pins': [1, 2],
    'labels': {'1': 'old1', '9': 'keep9'},
    'rules': [
      {'address': 1, 'repr': 'u16'},
      {'address': 9, 'repr': 'i16'},
    ],
    'samples': 3,
  },
};
Map<String, dynamic> started(String run, {int unit = 1}) => {
  'seq': 0,
  'kind': 'started',
  'run_id': run,
  'data': {
    'source': {
      'request_id': 'meter',
      'endpoint': 'tcp://actual:502',
      'unit': unit,
      'action': 'read-holding',
    },
  },
};
Map<String, dynamic> sample(
  String run,
  int seq,
  int address, {
  String value = '18446744073709551615',
}) => {
  'seq': seq,
  'run_id': run,
  'kind': 'registers',
  'time': '2026-10-01T05:00:00Z',
  'data': [
    {'address': address, 'u16': 65535, 'u64': value},
  ],
};

void main() {
  test('all integer edges remain exact and reject one past edge', () {
    for (final entry in {
      'u16': ['0', '65535'],
      'i16': ['-32768', '32767'],
      'u32': ['0', '4294967295'],
      'i32': ['-2147483648', '2147483647'],
      'u64': ['0', '18446744073709551615'],
      'i64': ['-9223372036854775808', '9223372036854775807'],
    }.entries) {
      for (final value in entry.value) {
        expect(exactTypedValue(entry.key, value), value);
      }
      expect(
        () => exactTypedValue(
          entry.key,
          (BigInt.parse(entry.value.first) - BigInt.one).toString(),
        ),
        throwsFormatException,
      );
      expect(
        () => exactTypedValue(
          entry.key,
          (BigInt.parse(entry.value.last) + BigInt.one).toString(),
        ),
        throwsFormatException,
      );
    }
    expect(() => exactTypedValue('u64', '1.0'), throwsFormatException);
    expect(() => exactTypedValue('f64', 'Infinity'), throwsFormatException);
    expect(exactTypedValue('u64', '9007199254740993'), '9007199254740993');
  });
  test(
    'navigation supports radix relative and unique labels without overflow',
    () {
      expect(parseModbusAddress('0xFFFF', 0, 1, {}), 65535);
      expect(parseModbusAddress('+0x10', 12, 1, {}), 28);
      expect(parseModbusAddress('-2', 12, 1, {}), 10);
      expect(parseModbusAddress('温度', 0, 2, {'12': '温度'}), 12);
      expect(
        () => parseModbusAddress('温度', 0, 1, {'1': '温度', '2': '温度'}),
        throwsFormatException,
      );
      for (final value in ['-1', '65536', '9999999999999999999999999']) {
        expect(
          () => parseModbusAddress(value, 0, 1, {}),
          throwsFormatException,
        );
      }
      expect(
        () => parseModbusAddress('65535', 0, 2, {}),
        throwsFormatException,
      );
    },
  );
  test(
    'annotation merge unions pins preserves unrelated entries and source',
    () {
      final original = request();
      final incoming = {
        'pins': [2, 3],
        'labels': {'1': 'new1', '3': 'new3'},
        'rules': [
          {'address': 1, 'repr': 'u32'},
          {'address': 3, 'repr': 'i32'},
        ],
        'unit': 8,
        'endpoint': 'bad',
        'samples': 900,
      };
      final keep = mergeAnnotations(original, 'read-holding', incoming);
      final replace = mergeAnnotations(
        original,
        'read-holding',
        incoming,
        overwriteLabels: {1},
        overwriteRules: {1},
      );
      expect(keep['params']['pins'], [1, 2, 3]);
      expect(keep['params']['labels'], {
        '1': 'old1',
        '3': 'new3',
        '9': 'keep9',
      });
      expect(replace['params']['labels']['1'], 'new1');
      expect(replace['params']['rules'], [
        {'address': 1, 'repr': 'u32'},
        {'address': 3, 'repr': 'i32'},
        {'address': 9, 'repr': 'i16'},
      ]);
      expect(replace['params']['unit'], 1);
      expect(replace['params']['samples'], 3);
      expect(original, request());
      expect(
        () => mergeAnnotations(original, 'read-input', incoming),
        throwsFormatException,
      );
    },
  );
  test('column models reject duplicate unknown and out of bounds widths', () {
    validateColumns({
      'visible': ['address', 'u64'],
      'widths': {'u64': 30},
      'address_mode': 'hex',
    });
    for (final value in [
      {
        'visible': ['u16', 'u16'],
      },
      {
        'visible': ['secret'],
      },
      {'visible': []},
      {
        'visible': ['u16'],
        'widths': {'u16': 121},
      },
      {
        'visible': ['u16'],
        'extra': 0,
      },
    ]) {
      expect(() => validateColumns(value), throwsFormatException);
    }
  });
  test('cache refuses draft provenance and keeps each response separate', () {
    final cache = ModbusCache();
    cache.add(sample('a', 1, 1));
    expect(cache.frames, isEmpty);
    cache.add(started('a'));
    cache.add(sample('a', 2, 1));
    cache.add(sample('a', 3, 2));
    expect(cache.frames.length, 2);
    expect(cache.frames[0].words, {'1': 65535});
    expect(cache.frames[1].words, {'2': 65535});
    final snapshot = cache.frames.first.snapshot();
    expect(snapshot['endpoint'], 'tcp://actual:502');
    expect(snapshot['unit'], 1);
    expect(snapshot['values'], {'1': 65535});
    cache.add(started('b', unit: 2));
    cache.add(sample('b', 1, 1));
    expect(cache.forScope('tcp://actual:502\n1\nholding').length, 2);
    expect(cache.forScope('tcp://actual:502\n2\nholding').length, 1);
  });
  test('cache is bounded and samples are immutable after caller mutation', () {
    final cache = ModbusCache()..add(started('a'));
    final event = sample('a', 1, 1);
    cache.add(event);
    (event['data'] as List).first['u64'] = '0';
    expect(cache.frames.first.rows.first['u64'], '18446744073709551615');
    for (var i = 2; i < 100; i++) {
      cache.add(sample('a', i, 1));
    }
    expect(cache.frames.length, 64);
    expect(cache.evicted, 35);
  });
  test('trends preserve exact integer stats and leave response gaps', () {
    final cache = ModbusCache()..add(started('a'));
    cache.add(sample('a', 1, 1, value: '18446744073709551614'));
    cache.add(sample('a', 2, 2));
    cache.add(sample('a', 3, 1));
    final trend = ModbusTrend(cache.frames, 1, 'u64');
    expect(trend.points[1], isNull);
    expect(trend.minimum.toString(), '18446744073709551614');
    expect(trend.maximum.toString(), '18446744073709551615');
    expect(trend.average, '18446744073709551614.5');
    final frozen = List<ModbusFrame>.of(cache.frames);
    cache.add(sample('a', 4, 1, value: '0'));
    expect(ModbusTrend(frozen, 1, 'u64').now, '18446744073709551615');
  });
  test('CSV guards formulas without corrupting exact signed integers', () {
    expect(csvCell('=1+1'), '"\'=1+1"');
    expect(csvCell('-9223372036854775808'), '"-9223372036854775808"');
    expect(csvCell('18446744073709551615'), '"18446744073709551615"');
    expect(csvCell('hello,"world"'), '"hello,""world"""');
  });
}
