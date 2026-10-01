import 'dart:convert';
import 'dart:io';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';
import '../../engine.dart';
import '../../json.dart';
import 'private_paths.dart';
import 'native_files.dart';

class NativePlatformServices implements PlatformServices {
  NativePlatformServices({
    this.rootDirectory,
    this.dialogs,
    this.iosChannel = const MethodChannel('io.github.liveyum.iotools/platform'),
  });
  final String? rootDirectory;
  final NativeFileDialogs? dialogs;
  final MethodChannel iosChannel;
  Future<String>? _root;
  NativeFiles? _files;
  Future<void> _settingsWrite = Future<void>.value();

  Future<String> privateRoot() => _root ??= _prepareRoot();
  Future<String> _prepareRoot() async {
    final selected = Directory(
      rootDirectory ??
          p.join((await getApplicationSupportDirectory()).path, 'iotools'),
    );
    await selected.create(recursive: true);
    final root = await selected.resolveSymbolicLinks();
    _files = NativeFiles(PrivatePaths(root), dialogs: dialogs);
    return root;
  }

  Future<JsonMap> _settings() async {
    final root = await privateRoot();
    final file = await _files!.privateFile(
      '.iotools-settings.json',
      existing: false,
    );
    JsonMap saved = {};
    if (await file.exists()) {
      if (await file.length() > 16384) throw const EngineException('本机设置文件过大');
      saved = mapOf(jsonDecode(await file.readAsString()));
    }
    return {
      'root': root.replaceAll('\\', '/'),
      'platform': Platform.operatingSystem,
      'version': const String.fromEnvironment(
        'IOTOOLS_VERSION',
        defaultValue: '0.3.0',
      ),
      'sha': const String.fromEnvironment(
        'IOTOOLS_SHA',
        defaultValue: 'unknown',
      ),
      'theme': saved['theme'] ?? 'dark',
      'readOnly': saved['readOnly'] == true,
      'history': saved['history'] == true,
      'collection': saved['collection'] ?? 'iotools.yaml',
      'usb': false,
      'capabilities': {
        'native_protocols': true,
        'serial': !Platform.isIOS,
        'usb': false,
        'file_import': true,
        'file_export': true,
        'large_file_streaming': true,
        'max_transfer_bytes': nativeMaxFileLimit,
        'background_policy': 'cancel_when_hidden',
      },
    };
  }

  Future<void> _save(JsonMap args) async {
    final root = await privateRoot();
    final values = <String, dynamic>{};
    for (final entry in args.entries) {
      switch (entry.key) {
        case 'theme':
          if (!['dark', 'light', 'system'].contains(entry.value))
            throw const EngineException('外观设置无效');
        case 'readOnly':
        case 'history':
          if (entry.value is! bool) throw const EngineException('开关设置必须是布尔值');
        case 'collection':
          if (entry.value is! String)
            throw const EngineException('集合设置必须是相对文件路径');
          final value = entry.value as String;
          if (p.isAbsolute(value)) throw const EngineException('集合设置必须是相对文件路径');
          PrivatePaths(root).handle(value);
          await _files!.privateFile(value, existing: false);
        default:
          throw EngineException('不支持的设置：${entry.key}');
      }
      values[entry.key] = entry.value;
    }
    final old = await _settings();
    final saved = {
      for (final key in ['theme', 'readOnly', 'history', 'collection'])
        key: values[key] ?? old[key],
    };
    final file = await _files!.privateFile(
      '.iotools-settings.json',
      existing: false,
    );
    final temporary = File('${file.path}.tmp');
    if (await FileSystemEntity.type(temporary.path, followLinks: false) ==
        FileSystemEntityType.link)
      throw const EngineException('设置临时文件不可为链接');
    await temporary.writeAsString(jsonEncode(saved), flush: true);
    await temporary.rename(file.path);
  }

  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    await privateRoot();
    switch (method) {
      case 'settings.get':
        return _settings();
      case 'settings.save':
        final result = _settingsWrite.then((_) => _save(args));
        _settingsWrite = result.then<void>(
          (_) {},
          onError: (Object _, StackTrace __) {},
        );
        await result;
        return null;
      case 'files.list':
        return _files!.list();
      case 'files.read':
        return _files!.read(args);
      case 'files.pick':
      case 'files.importBundle':
        return _files!.transfer(method, args);
      case 'files.export':
        if (Platform.isIOS) return iosChannel.invokeMethod(method, args);
        return _files!.transfer(method, args);
      case 'files.cancel':
        await _files!.cancel();
        if (Platform.isIOS) await iosChannel.invokeMethod('files.cancel');
        return null;
      case 'files.write':
        throw const EngineException('配置请通过引擎校验后保存');
      case 'usb.list':
        return '[]';
      case 'usb.status':
        return '当前平台没有 Android USB 适配器';
      case 'usb.permission':
      case 'usb.open':
      case 'usb.close':
        throw const EngineException('当前平台不支持 Android USB 接口，请使用平台串口或 TCP');
      case 'help.read':
        return rootBundle.loadString('lib/core/platform/native/assets/help.md');
      case 'licenses.read':
        return rootBundle.loadString('lib/core/platform/native/assets/LICENSE');
      default:
        throw EngineException('此平台不支持操作：$method');
    }
  }
}
