import 'dart:async';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';

class FakePlatform implements PlatformServices {
  final calls = <String>[];
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    calls.add(method);
    return method == 'settings.get'
        ? {'theme': 'dark', 'collection': 'iotools.yaml'}
        : null;
  }
}

class FakeEngine implements Engine {
  final calls = <JsonMap>[];
  Completer<Object?>? pendingEvents;
  bool closed = false;
  int pauseCount = 0;
  JsonMap state = {
    'version': 'test-sha',
    'profiles': ['本地'],
    'profile': '本地',
    'running': false,
    'options': {'history': false, 'read_only': false},
    'requests': [
      {
        'id': 'http_demo',
        'name': '设备状态查询',
        'protocol': 'http',
        'action': 'GET',
        'endpoint': 'http://127.0.0.1:8080/status',
        'timeout': '10s',
        'params': {},
      },
      {
        'id': 'mqtt_demo',
        'name': '车间温度订阅',
        'protocol': 'mqtt',
        'action': 'subscribe',
        'endpoint': 'tcp://127.0.0.1:1883',
        'params': {'topic': 'factory/temperature', 'qos': 0},
      },
      {
        'id': 'modbus_demo',
        'name': 'PLC 寄存器',
        'protocol': 'modbus',
        'action': 'read-holding',
        'endpoint': 'tcp://127.0.0.1:502',
        'params': {'unit': 1, 'address': 0, 'count': 8},
      },
    ],
  };
  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) async => cloneMap(state);
  @override
  Future<Object?> command(JsonMap c) async {
    calls.add(cloneMap(c));
    switch (c['op']) {
      case 'state':
        return cloneMap(state);
      case 'catalog':
        return {
          'protocols': [
            for (final p in ['http', 'mqtt', 'kafka', 'modbus', 'opcua'])
              {
                'id': p,
                'name': p.toUpperCase(),
                'actions': [
                  for (final a
                      in p == 'http'
                          ? ['GET', 'POST']
                          : p == 'mqtt'
                          ? ['subscribe', 'publish']
                          : p == 'modbus'
                          ? ['read-holding', 'write-register']
                          : ['read'])
                    {'id': a, 'name': a, 'defaults': {}},
                ],
                'fields': [
                  {'key': 'body', 'label': '请求体', 'type': 'text'},
                  {'key': 'headers', 'label': '请求头', 'type': 'json'},
                ],
              },
          ],
        };
      case 'config.get':
        return {'source': 'version: 1\nrequests: []\n'};
      case 'preview':
        return {
          'token': 'nonce',
          'request': c['request'],
          'confirmation_required': mapOf(c['request'])['action'] == 'POST',
        };
      case 'run':
        state['running'] = true;
        return {'run_id': 'opaque-run'};
      case 'events':
        if (pendingEvents != null) return pendingEvents!.future;
        return {'events': [], 'running': state['running']};
      case 'request.save':
        return cloneMap(state);
      case 'options.set':
        state['options'] = c['options'];
        return cloneMap(state);
      case 'history.list':
        return [];
      default:
        return {};
    }
  }

  @override
  Future<void> pause() async {
    pauseCount++;
    state['running'] = false;
  }

  @override
  Future<void> resume() async {}
  @override
  Future<void> close() async {
    closed = true;
  }
}
