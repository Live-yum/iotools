import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'package:iotools_mobile/features/files/file_tools.dart';
import 'support/fake_engine.dart';

class FilesPlatform extends FakePlatform {
  final args = <JsonMap>[];
  Completer<Object?>? pending;
  @override
  Future<Object?> invoke(String method, [JsonMap arguments = const {}]) async {
    calls.add(method);
    args.add(arguments);
    if (method == 'files.pick')
      return pending != null
          ? pending!.future
          : {'path': '/private/附件.bin', 'name': '附件.bin', 'size': 3};
    if (method == 'files.read') return 'version: 1\n# 中文😀\nrequests: []\n';
    return method == 'settings.get'
        ? {'root': '/private', 'theme': 'dark', 'collection': 'iotools.yaml'}
        : null;
  }
}

class ImportEngine extends FakeEngine {
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'config.import') {
      calls.add(cloneMap(c));
      return {
        'source': c['source'],
        'collection': {'requests': []},
      };
    }
    return super.command(c);
  }
}

void main() {
  testWidgets(
    'files.read String reaches configuration import intact, not a record',
    (t) async {
      final e = ImportEngine(), p = FilesPlatform();
      await t.pumpWidget(IotoolsApp(engine: e, platform: p));
      await t.pumpAndSettle();
      await t.tap(find.byKey(const ValueKey('nav_settings')));
      await t.pumpAndSettle();
      final f = find.text('导入配置 / 转换格式');
      await t.ensureVisible(f);
      await t.tap(f);
      await t.pumpAndSettle();
      await t.tap(find.text('预览转换'));
      await t.pumpAndSettle();
      expect(
        e.calls.lastWhere((c) => c['op'] == 'config.import')['source'],
        'version: 1\n# 中文😀\nrequests: []\n',
      );
      await t.tap(find.text('取消'));
      await t.pumpAndSettle();
      await t.pumpWidget(const SizedBox());
      await t.pump();
    },
  );
  testWidgets(
    'cancelled streaming transfer returns null and requests cleanup',
    (t) async {
      final p = FilesPlatform()..pending = Completer();
      Object? result = 'unset';
      await t.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () async {
                  result = await transferFile(c, p, 'files.pick', {
                    'limit': 8 * gib,
                  });
                },
                child: const Text('导入'),
              ),
            ),
          ),
        ),
      );
      await t.tap(find.text('导入'));
      await t.pump(const Duration(milliseconds: 400));
      await t.tap(find.text('停止传输'));
      await t.pump();
      expect(p.calls, contains('files.cancel'));
      p.pending!.complete({'path': 'unwanted'});
      await t.pumpAndSettle();
      expect(result, isNull);
      expect(p.args.first['limit'], 8 * gib);
    },
  );
  testWidgets(
    'multipart binary selection produces exact file reference with 4MiB bound',
    (t) async {
      final p = FilesPlatform(), s = AppSession(FakeEngine(), p);
      JsonMap? result;
      await t.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () async {
                  result = await editMultipart(c, s, {'text': '  中文😀  '});
                },
                child: const Text('编辑'),
              ),
            ),
          ),
        ),
      );
      await t.tap(find.text('编辑'));
      await t.pumpAndSettle();
      await t.tap(find.text('选择二进制文件字段'));
      await t.pumpAndSettle();
      await t.enterText(find.byKey(const ValueKey('input_dialog')), 'binary');
      await t.tap(find.text('应用'));
      await t.pumpAndSettle();
      await t.tap(find.text('应用到表单'));
      await t.pumpAndSettle();
      expect(result!['text'], '  中文😀  ');
      expect(result!['binary'], '{{ file("/private/附件.bin") }}');
      expect(p.args.first['limit'], multipartLimit);
      s.dispose();
    },
  );
}
