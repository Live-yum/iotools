import 'dart:convert';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const engineChannel = MethodChannel('io.github.liveyum.iotools/engine');
  const platformChannel = MethodChannel('io.github.liveyum.iotools/platform');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(() {
    messenger.setMockMethodCallHandler(engineChannel, null);
    messenger.setMockMethodCallHandler(platformChannel, null);
  });

  test('UTF-8 and UInt64 remain exact JSON bytes through engine channel', () async {
    MethodCall? captured;
    messenger.setMockMethodCallHandler(engineChannel, (call) async {
      captured = call;
      return '{"ok":true,"data":{"value":"18446744073709551615","text":"中文😀"}}';
    });
    final reply = await const MethodChannelEngine().command({
      'op': 'preview',
      'request': {'id': '草稿😀', 'params': {'value': const ExactNumber('18446744073709551615')}},
    });
    expect(captured!.method, 'command');
    expect(captured!.arguments['json'], isA<String>());
    expect(captured!.arguments['json'], contains('18446744073709551615'));
    expect(captured!.arguments['json'], contains('草稿😀'));
    expect(mapOf(reply)['value'], '18446744073709551615');
    expect(mapOf(reply)['text'], '中文😀');
  });

  test('finite decimal replies stay JSON-compatible while large integers stay strings', () async {
    messenger.setMockMethodCallHandler(engineChannel, (_) async => '{"ok":true,"data":{"pressure":101.25,"counter":"18446744073709551615"}}');
    final reply = mapOf(await const MethodChannelEngine().command({'op':'events'}));
    expect(reply['pressure'], 101.25);
    expect(reply['counter'], '18446744073709551615');
    expect(() => jsonEncode(reply), returnsNormally);
  });

  test('open explicitly conveys read-only history and private collection path', () async {
    messenger.setMockMethodCallHandler(engineChannel, (call) async {
      expect(call.method, 'open');
      expect(call.arguments, {'readOnly': true, 'history': false, 'path': '/private/中文.yaml'});
      return '{"ok":true,"data":{"path":"/private/中文.yaml","requests":[]}}';
    });
    final state = await const MethodChannelEngine().open(readOnly: true, path: '/private/中文.yaml');
    expect(state['path'], '/private/中文.yaml');
  });

  test('engine failures do not become successful result payloads', () async {
    messenger.setMockMethodCallHandler(engineChannel, (_) async => '{"ok":false,"error":"只读模式禁止写入"}');
    await expectLater(const MethodChannelEngine().command({'op':'run'}), throwsA(isA<EngineException>()));
  });

  test('pause resume close issue no command or implicit request replay', () async {
    final calls = <String>[];
    messenger.setMockMethodCallHandler(engineChannel, (call) async { calls.add(call.method); return null; });
    const engine = MethodChannelEngine();
    await engine.pause(); await engine.resume(); await engine.close();
    expect(calls, ['pause', 'resume', 'close']);
  });

  test('file picker cancellation is null and streaming limits stay 64-bit', () async {
    messenger.setMockMethodCallHandler(platformChannel, (call) async {
      expect(call.method, 'files.pick');
      expect(call.arguments['limit'], 8589934592);
      return null;
    });
    expect(await const MethodChannelPlatform().invoke('files.pick', {'limit':8589934592}), isNull);
  });

  test('USB permission and open are separate explicit operations', () async {
    final calls = <String>[];
    messenger.setMockMethodCallHandler(platformChannel, (call) async {
      calls.add(call.method);
      expect(call.arguments['endpoint'], 'usb://123/0');
      return call.method=='usb.permission' ? false : '已打开';
    });
    const platform = MethodChannelPlatform();
    expect(await platform.invoke('usb.permission', {'endpoint':'usb://123/0'}), false);
    expect(calls, ['usb.permission']);
    await platform.invoke('usb.open', {'endpoint':'usb://123/0','baud':9600,'dataBits':8,'stopBits':1,'parity':'N'});
    expect(calls, ['usb.permission','usb.open']);
  });
}
