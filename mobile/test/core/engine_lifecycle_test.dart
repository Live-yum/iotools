import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/engine.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('iotools/lifecycle-test');
  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });
  test(
    'delayed dispose close finishes before reopen and following command',
    () async {
      final closeGate = Completer<void>();
      final calls = <String>[];
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            calls.add(call.method);
            if (call.method == 'close') await closeGate.future;
            return {'ok': true, 'data': <String, dynamic>{}};
          });
      const a = MethodChannelEngine(channel), b = MethodChannelEngine(channel);
      final closing = a.close();
      final opening = b.open();
      final command = b.command({'op': 'config.get'});
      await Future<void>.delayed(Duration.zero);
      expect(calls, ['close']);
      closeGate.complete();
      await closing;
      await opening;
      await command;
      expect(calls, ['close', 'open', 'command']);
    },
  );
  test(
    'failed lifecycle reports error and does not poison later open',
    () async {
      var fail = true;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            if (fail) {
              fail = false;
              throw PlatformException(code: 'fixture');
            }
            return {
              'ok': true,
              'data': <String, dynamic>{'version': 'recovered'},
            };
          });
      const engine = MethodChannelEngine(channel);
      await expectLater(engine.close(), throwsA(isA<PlatformException>()));
      expect((await engine.open())['version'], 'recovered');
    },
  );
}
