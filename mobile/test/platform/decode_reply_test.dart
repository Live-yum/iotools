import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('iotools-test/exact-reply');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));
  for (final large in [false, true]) {
    test(
      'MethodChannel ${large ? 'worker' : 'inline'} decode retains HTTP JSON number versus string',
      () async {
        String? preview;
        messenger.setMockMethodCallHandler(channel, (call) async {
          final input = mapOf(exactDecode(call.arguments['json']));
          if (input['op'] == 'preview') {
            preview = call.arguments['json'];
            return '{"ok":true,"data":{"token":"t"}}';
          }
          return '{"ok":true,"data":{"request":{"protocol":"http","id":"n","name":"before","endpoint":"http://localhost","params":{"json":{"number":18446744073709551615,"string":"18446744073709551615","decimal":0.1234567890123456789,"nested":{"protocol":"http","params":{"value":18446744073709551615}}}}},"duration":1.25,"padding":"${large ? 'x' * 270000 : ''}"}}';
        });
        const engine = MethodChannelEngine(channel);
        final reply = mapOf(await engine.command({'op': 'config.get'}));
        final request = cloneMap(mapOf(reply['request']))..['name'] = 'after';
        expect(reply['duration'], 1.25);
        final json = mapOf(mapOf(request['params'])['json']);
        expect(json['number'], isA<ExactNumber>());
        expect(json['string'], isA<String>());
        await engine.command({'op': 'preview', 'request': request});
        expect(preview, contains('"number":18446744073709551615'));
        expect(preview, contains('"string":"18446744073709551615"'));
        expect(preview, contains('"decimal":0.1234567890123456789'));
        expect(preview, contains('"value":18446744073709551615'));
      },
    );
  }
}
