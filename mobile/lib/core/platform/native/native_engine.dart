import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';
import 'package:ffi/ffi.dart';
import 'package:path/path.dart' as p;
import '../../engine.dart';
import '../../json.dart';
import 'private_paths.dart';

typedef _OpenNative =
    Uint64 Function(
      Pointer<Uint8>,
      UintPtr,
      Pointer<Uint8>,
      UintPtr,
      Uint32,
      Pointer<Pointer<Uint8>>,
      Pointer<UintPtr>,
    );
typedef _OpenDart =
    int Function(
      Pointer<Uint8>,
      int,
      Pointer<Uint8>,
      int,
      int,
      Pointer<Pointer<Uint8>>,
      Pointer<UintPtr>,
    );
typedef _CommandNative =
    Int32 Function(
      Uint64,
      Pointer<Uint8>,
      UintPtr,
      Pointer<Pointer<Uint8>>,
      Pointer<UintPtr>,
    );
typedef _CommandDart =
    int Function(
      int,
      Pointer<Uint8>,
      int,
      Pointer<Pointer<Uint8>>,
      Pointer<UintPtr>,
    );
typedef _LifecycleNative = Int32 Function(Uint64, Int32);
typedef _LifecycleDart = int Function(int, int);
typedef _FreeNative = Void Function(Pointer<Uint8>);
typedef _FreeDart = void Function(Pointer<Uint8>);

String nativeLibraryPath() {
  final folder = p.dirname(Platform.resolvedExecutable);
  if (Platform.isIOS) return '';
  if (Platform.isWindows) return p.join(folder, 'iotools_native.dll');
  if (Platform.isMacOS)
    return p.normalize(
      p.join(folder, '..', 'Frameworks', 'libiotools_native.dylib'),
    );
  if (Platform.isLinux) return p.join(folder, 'lib', 'libiotools_native.so');
  throw const EngineException('此平台不支持原生协议内核');
}

/// Each operation runs outside Flutter's UI isolate. A long local command does
/// not prevent a second isolate from calling native pause/cancel/close.
class NativeFfiEngine implements Engine {
  NativeFfiEngine({required this.root, this.libraryPath});
  final Future<String> Function() root;
  final String? libraryPath;
  Future<void> _lifecycle = Future<void>.value();
  int _handle = 0;
  String? _root;

  Future<T> _ordered<T>(Future<T> Function() work) {
    final next = _lifecycle.then((_) => work());
    _lifecycle = next.then<void>((_) {}, onError: (Object _, StackTrace __) {});
    return next;
  }

  Future<Map<String, Object?>> _invoke(Map<String, Object?> message) {
    final data = {...message, 'library': libraryPath ?? nativeLibraryPath()};
    return Isolate.run(() => _nativeCall(data));
  }

  Object? _unwrap(Map<String, Object?> reply) {
    final envelope = mapOf(reply['reply']);
    if (envelope['ok'] != true) {
      throw EngineException(envelope['error']?.toString() ?? '原生内核返回无效响应');
    }
    final value = envelope['data'];
    // Only collection metadata is converted; payload strings and file values
    // in protocol results remain the exact engine response.
    if (value is Map && value['path'] is String && _root != null) {
      final copy = Map<String, dynamic>.from(value);
      copy['path'] = PrivatePaths(_root!).handle(value['path'] as String);
      return copy;
    }
    return value;
  }

  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) => _ordered(() async {
    if (_handle != 0) {
      final old = _handle;
      _handle = 0;
      await _invoke({'op': 'lifecycle', 'handle': old, 'action': 2});
    }
    // The trusted OS directory may use a platform alias (/var on macOS, or a
    // Windows short path). Match the canonical root also used by Go before
    // deriving portable handles; never canonicalize arbitrary JSON paths.
    _root = await Directory(await root()).resolveSymbolicLinks();
    final selected = PrivatePaths(_root!).resolve(path ?? 'iotools.yaml');
    final response = await _invoke({
      'op': 'open',
      'path': selected,
      'root': _root,
      'flags': (readOnly ? 1 : 0) | (history ? 2 : 0) | (path != null ? 4 : 0),
    });
    _handle = response['handle'] as int? ?? 0;
    return mapOf(_unwrap(response));
  });

  @override
  Future<Object?> command(JsonMap command) async {
    await _lifecycle;
    if (_handle == 0) throw const EngineException('请先打开工作区');
    return _unwrap(
      await _invoke({
        'op': 'command',
        'handle': _handle,
        'json': exactEncode(command),
      }),
    );
  }

  Future<void> _change(int action) => _ordered(() async {
    if (_handle == 0) return;
    final handle = _handle;
    if (action == 2) _handle = 0;
    await _invoke({'op': 'lifecycle', 'handle': handle, 'action': action});
  });
  @override
  Future<void> pause() => _change(0);
  @override
  Future<void> resume() => _change(1);
  @override
  Future<void> close() => _change(2);
}

Map<String, Object?> _nativeCall(Map<String, Object?> message) {
  final path = message['library']! as String;
  final DynamicLibrary library;
  try {
    library = path.isEmpty
        ? DynamicLibrary.process()
        : DynamicLibrary.open(path);
  } catch (_) {
    throw const EngineException('未找到随应用打包的 Go 协议内核，请安装完整发行包');
  }
  final version = library.lookupFunction<Uint32 Function(), int Function()>(
    'IotoolsNativeABIVersion',
  )();
  if (version != 1) throw EngineException('协议内核接口版本不兼容：$version');
  final operation = message['op'];
  if (operation == 'lifecycle') {
    final status = library.lookupFunction<_LifecycleNative, _LifecycleDart>(
      'IotoolsNativeLifecycle',
    )(message['handle']! as int, message['action']! as int);
    if (status != 0 && !(status == 2 && message['action'] == 2)) {
      throw EngineException('内核生命周期操作失败（$status）');
    }
    return {};
  }
  final output = calloc<Pointer<Uint8>>();
  final length = calloc<UintPtr>();
  final release = library.lookupFunction<_FreeNative, _FreeDart>(
    'IotoolsNativeFree',
  );
  Pointer<Utf8>? input, root;
  try {
    var handle = message['handle'] as int? ?? 0;
    if (operation == 'open') {
      final selected = message['path']! as String;
      final directory = message['root']! as String;
      input = selected.toNativeUtf8();
      root = directory.toNativeUtf8();
      handle =
          library.lookupFunction<_OpenNative, _OpenDart>('IotoolsNativeOpen')(
            input.cast(),
            utf8.encode(selected).length,
            root.cast(),
            utf8.encode(directory).length,
            message['flags']! as int,
            output,
            length,
          );
    } else {
      final command = message['json']! as String;
      final bytes = utf8.encode(command).length;
      if (bytes > 8 * 1024 * 1024) throw const EngineException('命令超过 8 MiB 上限');
      input = command.toNativeUtf8();
      final status = library.lookupFunction<_CommandNative, _CommandDart>(
        'IotoolsNativeCommand',
      )(handle, input.cast(), bytes, output, length);
      if (status != 0) throw EngineException('内核调用失败（$status），请重新打开工作区');
    }
    if (output.value == nullptr ||
        length.value == 0 ||
        length.value > 16 * 1024 * 1024 + 65536) {
      throw const EngineException('原生内核返回长度无效');
    }
    final reply = decodeEngineReply(
      utf8.decode(output.value.asTypedList(length.value)),
    );
    return {'handle': handle, 'reply': reply};
  } finally {
    if (input != null) malloc.free(input);
    if (root != null) malloc.free(root);
    if (output.value != nullptr) release(output.value);
    calloc.free(output);
    calloc.free(length);
  }
}
