@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/platform/gateway.dart';

/// Browsers supply cookies and Origin themselves. The VM fixture emulates only
/// those HTTP transport details while exercising the real Go gateway.
class CookieFixtureClient extends http.BaseClient {
  CookieFixtureClient(this.origin);
  final Uri origin;
  final _client = http.Client();
  String? _cookie;
  @override Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'POST') request.headers['Origin'] = origin.origin;
    if (_cookie != null) request.headers['Cookie'] = _cookie!;
    final response = await _client.send(request);
    if (response.headers['set-cookie'] case final String cookie) _cookie = cookie.split(';').first;
    return response;
  }
  @override void close() => _client.close();
}

void main() {
  const address = String.fromEnvironment('IOTOOLS_GATEWAY_TEST_ORIGIN');
  test('real Go gateway handshake, config number types, preview and HTTP execution', () async {
    final origin = Uri.parse(address), client = CookieFixtureClient(Uri.parse(address));
    final transport = GatewayTransport(origin:origin,client:client);
    final engine = GatewayEngine(transport);
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4,0);
    final received = Completer<String>();
    var calls = 0;
    final subscription = server.listen((request) async {
      calls++;
      final body = await utf8.decoder.bind(request).join();
      if(!received.isCompleted)received.complete(body);
      request.response.write('网关真实响应😀');
      await request.response.close();
    });
    String? original;
    try {
      expect(mapOf(await transport.bootstrap())['csrf'],isNotEmpty);
      final settings=mapOf(await transport.post('/api/platform',{'method':'settings.get','args':{}}));
      expect(settings['platform'],'web');
      await engine.open();
      original=mapOf(await engine.command({'op':'config.get'}))['source'].toString();
      await engine.command({'op':'config.save','source':'''version: 1
requests:
  - id: web-exact
    name: before
    protocol: http
    action: POST
    endpoint: http://127.0.0.1:${server.port}/echo
    params:
      json:
        number: 18446744073709551615
        string: "18446744073709551615"
'''});
      final config=mapOf(await engine.command({'op':'config.get'}));
      final request=cloneMap(rowsOf(mapOf(config['collection'])['requests']).single)..['name']='仅修改名称';
      expect(mapOf(mapOf(request['params'])['json'])['number'],isA<ExactNumber>());
      expect(mapOf(mapOf(request['params'])['json'])['string'],isA<String>());
      final preview=mapOf(await engine.command({'op':'preview','request':request}));
      expect(calls,0);
      final run=mapOf(await engine.command({'op':'run','token':preview['token'],'confirmed':true}));
      final body=await received.future.timeout(const Duration(seconds:5));
      expect(body,contains('"number":18446744073709551615'));
      expect(body,contains('"string":"18446744073709551615"'));
      var done=false;
      final deadline=DateTime.now().add(const Duration(seconds:5));
      while(DateTime.now().isBefore(deadline)){
        final events=rowsOf(mapOf(await engine.command({'op':'events'}))['events']);
        if(events.any((event)=>event['run_id']==run['run_id']&&event['kind']=='done')){done=true;break;}
        await Future<void>.delayed(const Duration(milliseconds:20));
      }
      expect(done,true);expect(calls,1);
      await engine.pause();await engine.resume();
      expect(mapOf(await engine.command({'op':'state'}))['running'],false);expect(calls,1);
    } finally {
      if(original!=null)await engine.command({'op':'config.save','source':original});
      await engine.close();client.close();await subscription.cancel();await server.close(force:true);
    }
  },skip:address.isEmpty?'requires an explicitly started local Go gateway':false);
}
