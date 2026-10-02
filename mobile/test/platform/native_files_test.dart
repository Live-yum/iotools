import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:archive/archive.dart';
import 'package:file_selector/file_selector.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/platform/native/native_files.dart';
import 'package:iotools_mobile/core/platform/native/native_platform.dart';
import 'package:iotools_mobile/core/platform/native/private_paths.dart';

class Dialogs implements NativeFileDialogs {
  XFile? selection;
  String? destination;
  Completer<XFile?>? waiting;
  @override
  Future<XFile?> pick({required bool bundle}) async =>
      waiting == null ? selection : await waiting!.future;
  @override
  Future<String?> save(String name) async => destination;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('Windows picker names use both separator styles without leaking temporary directories', () {
    final windows=p.Context(style:p.Style.windows);
    expect(pickedFilename(r'C:\Temp\picker/中文😀.yaml',context:windows),'中文😀.yaml');
    expect(pickedFilename(r'picker\中文😀.yaml',context:windows),'中文😀.yaml');
  });
  late Directory root, external;
  late Dialogs dialogs;
  late NativeFiles files;
  setUp(() async {
    final created = await Directory.systemTemp.createTemp('iotools-private-test-');
    // The file service receives the host's canonical trusted root. Absolute
    // test files must use that same root, not a macOS /var or Windows 8.3 alias.
    root = Directory(await created.resolveSymbolicLinks());
    external = await Directory.systemTemp.createTemp('iotools-picker-test-');
    dialogs = Dialogs();
    files = NativeFiles(
      PrivatePaths(root.path),
      dialogs: dialogs,
    );
  });
  tearDown(() async {
    await root.delete(recursive: true);
    await external.delete(recursive: true);
  });

  test(
    'import returns relative handle with exact UTF8; cancel picker changes no files',
    () async {
      expect(await files.transfer('files.pick', {}), isNull);
      expect(await files.list(), isEmpty);
      final source = await File(
        '${external.path}/中文😀.yaml',
      ).writeAsString('name: 工业😀\n');
      dialogs.selection = XFile(source.path);
      final result = mapOf(await files.transfer('files.pick', {'limit': 4096}));
      expect(result['path'], startsWith('attachment-'));
      expect(result['path'], endsWith('/中文😀.yaml'));
      expect(
        await files.read({'path': result['path'], 'limit': 4096}),
        'name: 工业😀\n',
      );
      expect(await source.readAsString(), 'name: 工业😀\n');
    },
  );
  test(
    'oversized imports and late picker results after cancel leave no attachment',
    () async {
      dialogs.selection = XFile.fromData(Uint8List(32), name: 'large.bin');
      await expectLater(
        files.transfer('files.pick', {'limit': 16}),
        throwsA(isA<EngineException>()),
      );
      expect(await files.list(), isEmpty);
      dialogs.waiting = Completer();
      final task = files.transfer('files.pick', {'limit': 128});
      final expectation = expectLater(task, throwsA(isA<EngineException>()));
      await Future<void>.delayed(Duration.zero);
      await files.cancel();
      dialogs.waiting!.complete(XFile.fromData(Uint8List(8), name: 'late.bin'));
      await expectation;
      expect(await root.list().toList(), isEmpty);
    },
  );
  test('export writes binary exactly and refuses existing targets', () async {
    final source = await File(
      '${root.path}/binary.bin',
    ).writeAsBytes([0, 255, 1, 128]);
    dialogs.destination = '${external.path}/result.bin';
    expect(
      await files.transfer('files.export', {
        'path': source.path,
        'name': 'result.bin',
      }),
      {'exported': true},
    );
    expect(await File(dialogs.destination!).readAsBytes(), [0, 255, 1, 128]);
    await expectLater(
      files.transfer('files.export', {'text': 'replacement'}),
      throwsA(isA<FileSystemException>()),
    );
    expect(await File(dialogs.destination!).readAsBytes(), [0, 255, 1, 128]);
  });
  test('private reads reject external files and symlinks', () async {
    final secret = await File(
      '${external.path}/outside.txt',
    ).writeAsString('outside');
    await expectLater(
      files.read({'path': secret.path, 'limit': 4096}),
      throwsA(isA<EngineException>()),
    );
    if (!Platform.isWindows) {
      await Link('${root.path}/link.txt').create(secret.path);
      await expectLater(
        files.read({'path': 'link.txt', 'limit': 4096}),
        throwsA(isA<EngineException>()),
      );
    }
  });
  Future<void> selectZip(Archive archive) async {
    final zip = await File(
      '${external.path}/bundle.zip',
    ).writeAsBytes(ZipEncoder().encode(archive));
    dialogs.selection = XFile(zip.path);
  }

  test(
    'ZIP imports configuration plus relative binary attachments with bounded output',
    () async {
      await selectZip(
        Archive()
          ..add(
            ArchiveFile(
              'folder/config.yaml',
              utf8.encode('中文\n').length,
              utf8.encode('中文\n'),
            ),
          )
          ..add(ArchiveFile('folder/data.bin', 3, [0, 255, 1])),
      );
      final result = mapOf(
        await files.transfer('files.importBundle', {'limit': 4096}),
      );
      expect(rowsOf(result['files']), hasLength(2));
      final data = rowsOf(
        result['files'],
      ).firstWhere((v) => v['name'] == 'data.bin');
      expect(await (await files.privateFile(data['path'])).readAsBytes(), [
        0,
        255,
        1,
      ]);
      expect(result['bytes'], utf8.encode('中文\n').length + 3);
    },
  );
  test('ZIP traversal and expansion excess remove staging directory', () async {
    await selectZip(Archive()..add(ArchiveFile('../escape.txt', 1, [1])));
    await expectLater(
      files.transfer('files.importBundle', {'limit': 4096}),
      throwsA(isA<EngineException>()),
    );
    expect(await root.list().toList(), isEmpty);
    await selectZip(
      Archive()..add(ArchiveFile('bomb.bin', 65536, Uint8List(65536))),
    );
    await expectLater(
      files.transfer('files.importBundle', {'limit': 4096}),
      throwsA(isA<EngineException>()),
    );
    expect(await root.list().toList(), isEmpty);
  });
  test('history defaults on and retains an explicit off across restarts', () async {
    NativePlatformServices fresh() => NativePlatformServices(
      rootDirectory: root.path, dialogs: dialogs,
    );
    expect(mapOf(await fresh().invoke('settings.get'))['history'], true);
    await File('${root.path}/.iotools-settings.json').writeAsString('{"theme":"light"}');
    expect(mapOf(await fresh().invoke('settings.get'))['history'], true);
    await fresh().invoke('settings.save', {'history': false});
    expect(mapOf(await fresh().invoke('settings.get'))['history'], false);
    await fresh().invoke('settings.save', {'theme': 'dark'});
    expect(mapOf(await fresh().invoke('settings.get'))['history'], false);
    await fresh().invoke('settings.save', {'history': true});
    expect(mapOf(await fresh().invoke('settings.get'))['history'], true);
  });
  test(
    'settings persist only approved keys and never accept external collections',
    () async {
      final platform = NativePlatformServices(
        rootDirectory: root.path,
        dialogs: dialogs,
      );
      await platform.invoke('settings.save', {
        'theme': 'light',
        'history': true,
        'collection': 'bundle/config.yaml',
      });
      final next = NativePlatformServices(
        rootDirectory: root.path,
        dialogs: dialogs,
      );
      final loaded = mapOf(await next.invoke('settings.get'));
      expect(loaded['theme'], 'light');
      expect(loaded['history'], true);
      expect(loaded['collection'], 'bundle/config.yaml');
      await expectLater(
        next.invoke('settings.save', {'password': 'secret'}),
        throwsA(isA<EngineException>()),
      );
      await expectLater(
        next.invoke('settings.save', {'collection': '../outside.yaml'}),
        throwsA(isA<EngineException>()),
      );
      final contents = await File(
        '${root.path}/.iotools-settings.json',
      ).readAsString();
      expect(contents, isNot(contains('secret')));
    },
  );
}
