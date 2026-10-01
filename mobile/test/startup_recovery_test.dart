import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/app/app.dart';
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';
import 'support/fake_engine.dart';

class RecoveryPlatform extends FakePlatform {
  final files = <String, String>{
    'iotools.yaml': 'version: [损坏的原配置',
    'invalid.yaml': 'requests: [损坏的候选',
    'valid.yaml': 'version: 1\nrequests: []\n',
  };
  final settings = <String, dynamic>{
    'root': '/private',
    'theme': 'dark',
    'collection': 'iotools.yaml',
    'readOnly': true,
    'history': false,
  };
  final writes = <JsonMap>[];
  final trace = <String>[];
  JsonMap? picked;
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    calls.add(method);
    switch (method) {
      case 'settings.get':
        return cloneMap(settings);
      case 'settings.save':
        trace.add('save:${args['collection']}');
        writes.add(cloneMap(args));
        settings.addAll(args);
        return true;
      case 'files.list':
        return [
          for (final file in files.entries)
            {
              'path': '/private/${file.key}',
              'name': file.key,
              'size': file.value.length,
            },
        ];
      case 'files.pick':
        expect(args['limit'], 4194304);
        return picked;
      default:
        throw StateError('恢复界面不应调用 $method');
    }
  }
}

class RecoveryEngine extends FakeEngine {
  RecoveryEngine(this.platform);
  final RecoveryPlatform platform;
  final opens = <JsonMap>[];
  Completer<void>? heldOpen;
  String? active;
  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) async {
    final selected = (path ?? 'iotools.yaml').replaceFirst('/private/', '');
    opens.add({'path': selected, 'readOnly': readOnly, 'history': history});
    platform.trace.add('open:$selected');
    final source = platform.files[selected];
    if (source == null || !source.startsWith('version: 1\n')) {
      throw EngineException('配置解析失败：$selected');
    }
    if (heldOpen != null) await heldOpen!.future;
    active = selected;
    platform.trace.add('validated:$selected');
    return {
      ...cloneMap(state),
      'path': '/private/$selected',
      'options': {'read_only': readOnly, 'history': history},
    };
  }

  @override
  Future<Object?> command(JsonMap command) async {
    if (command['op'] == 'config.get') {
      calls.add(cloneMap(command));
      if (active == null) throw const EngineException('没有已打开的集合');
      return {'source': platform.files[active], 'path': '/private/$active'};
    }
    return super.command(command);
  }
}

Future<void> tapRecovery(WidgetTester tester, Finder target) async {
  await tester.ensureVisible(target);
  await tester.pumpAndSettle();
  await tester.tap(target);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'startup retry, cancel, invalid choice and valid recovery use visible controls',
    (tester) async {
      final platform = RecoveryPlatform(), originalFiles = <String, String>{};
      originalFiles.addAll(platform.files);
      final engine = RecoveryEngine(platform);
      await tester.pumpWidget(IotoolsApp(engine: engine, platform: platform));
      await tester.pumpAndSettle();
      expect(find.text('无法打开配置'), findsOneWidget);
      expect(find.text('重试打开'), findsOneWidget);
      await tapRecovery(tester, find.byKey(const ValueKey('startup_retry')));
      expect(engine.opens.length, 2);
      expect(platform.writes, isEmpty);

      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_select_collection')),
      );
      await tapRecovery(tester, find.text('取消').last);
      expect(engine.opens.length, 2);
      expect(platform.settings['collection'], 'iotools.yaml');

      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_select_collection')),
      );
      await tapRecovery(tester, find.text('valid.yaml'));
      await tapRecovery(tester, find.text('取消').last);
      expect(engine.opens.length, 2);
      expect(platform.writes, isEmpty);

      final session = tester
          .widget<WorkspaceShell>(find.byType(WorkspaceShell))
          .session;
      session.source = '仅内存的未保存文字';
      session.drafts['unsaved'] = {'id': 'unsaved', 'name': '仅内存'};
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_select_collection')),
      );
      await tapRecovery(tester, find.text('invalid.yaml'));
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_use_collection')),
      );
      expect(find.textContaining('配置解析失败：invalid.yaml'), findsOneWidget);
      expect(session.source, '仅内存的未保存文字');
      expect(session.drafts['unsaved']!['name'], '仅内存');
      expect(platform.writes, isEmpty);
      expect(platform.settings['collection'], 'iotools.yaml');
      expect(platform.files, originalFiles);

      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_select_collection')),
      );
      await tapRecovery(tester, find.text('valid.yaml'));
      engine.heldOpen = Completer<void>();
      await tester.tap(find.byKey(const ValueKey('startup_use_collection')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pump();
      expect(engine.opens.last['path'], 'valid.yaml');
      expect(platform.writes, isEmpty, reason: '内核尚未完成校验');
      engine.heldOpen!.complete();
      await tester.pumpAndSettle();
      expect(find.text('请求工作台'), findsOneWidget);
      expect(platform.settings['collection'], 'valid.yaml');
      expect(platform.writes, [
        {'collection': 'valid.yaml'},
      ]);
      expect(
        platform.trace.indexOf('validated:valid.yaml'),
        lessThan(platform.trace.indexOf('save:valid.yaml')),
      );
      expect(engine.opens.last['readOnly'], true);
      expect(platform.files, originalFiles);
      expect(
        engine.calls.where(
          (c) => [
            'run',
            'preview',
            'config.save',
            'request.save',
          ].contains(c['op']),
        ),
        isEmpty,
      );
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      final reopened = RecoveryEngine(platform);
      await tester.pumpWidget(IotoolsApp(engine: reopened, platform: platform));
      await tester.pumpAndSettle();
      expect(find.text('请求工作台'), findsOneWidget);
      expect(reopened.opens.last['path'], 'valid.yaml');
      expect(platform.files, originalFiles);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );

  testWidgets(
    'startup import cancellation preserves original and valid copy needs confirmation',
    (tester) async {
      final platform = RecoveryPlatform();
      final actualEngine = RecoveryEngine(platform);
      await tester.pumpWidget(
        IotoolsApp(engine: actualEngine, platform: platform),
      );
      await tester.pumpAndSettle();
      final original = platform.files['iotools.yaml'];
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_import_collection')),
      );
      expect(actualEngine.opens.length, 1);
      expect(platform.writes, isEmpty);

      platform.files['imported-valid.yaml'] = 'version: 1\nrequests: []\n';
      platform.picked = {
        'path': '/private/imported-valid.yaml',
        'name': 'imported-valid.yaml',
        'size': 24,
      };
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_import_collection')),
      );
      expect(find.text('打开这个集合？'), findsOneWidget);
      await tapRecovery(tester, find.text('取消').last);
      expect(actualEngine.opens.length, 1);
      expect(platform.writes, isEmpty);
      expect(platform.settings['collection'], 'iotools.yaml');
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_import_collection')),
      );
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_use_collection')),
      );
      expect(find.text('请求工作台'), findsOneWidget);
      expect(platform.settings['collection'], 'imported-valid.yaml');
      expect(platform.files['iotools.yaml'], original);
      expect(actualEngine.calls.where((c) => c['op'] == 'run'), isEmpty);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );

  testWidgets(
    'backgrounding a recovery review cancels selection without network replay',
    (tester) async {
      final platform = RecoveryPlatform();
      final actualEngine = RecoveryEngine(platform);
      await tester.pumpWidget(
        IotoolsApp(engine: actualEngine, platform: platform),
      );
      await tester.pumpAndSettle();
      await tapRecovery(
        tester,
        find.byKey(const ValueKey('startup_select_collection')),
      );
      await tapRecovery(tester, find.text('valid.yaml'));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(find.text('打开这个集合？'), findsNothing);
      expect(find.text('无法打开配置'), findsOneWidget);
      expect(platform.writes, isEmpty);
      expect(platform.settings['collection'], 'iotools.yaml');
      expect(actualEngine.opens.length, 1);
      expect(actualEngine.calls, isEmpty);
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );
}
