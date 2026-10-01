import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:math';
import 'dart:typed_data';
import 'package:archive/archive_io.dart';
import 'package:file_selector/file_selector.dart';
import 'package:path/path.dart' as p;
import '../../engine.dart';
import '../../json.dart';
import 'private_paths.dart';

const nativeFileLimit = 1024 * 1024 * 1024;
const nativeMaxFileLimit = 8 * nativeFileLimit;

abstract interface class NativeFileDialogs {
  Future<XFile?> pick({required bool bundle});
  Future<String?> save(String name);
}

class SystemFileDialogs implements NativeFileDialogs {
  const SystemFileDialogs();
  @override
  Future<XFile?> pick({required bool bundle}) => openFile(
    acceptedTypeGroups: bundle
        ? const [
            XTypeGroup(
              label: 'ZIP 配置与附件',
              extensions: ['zip'],
              uniformTypeIdentifiers: ['public.zip-archive'],
            ),
          ]
        : const [],
  );
  @override
  Future<String?> save(String name) async =>
      (await getSaveLocation(suggestedName: name))?.path;
}

class _Transfer {
  _Transfer(this.marker);
  final File marker;
  bool cancelled = false;
  Future<void>? cancellation;
  void check() {
    if (cancelled) throw const EngineException('文件操作已取消');
  }

  Future<void> cancel() {
    cancelled = true;
    return cancellation ??= marker.writeAsString('cancel').then((_) {});
  }
}

class NativeFiles {
  NativeFiles(this.paths, {NativeFileDialogs? dialogs})
    : dialogs = dialogs ?? const SystemFileDialogs();
  final PrivatePaths paths;
  final NativeFileDialogs dialogs;
  _Transfer? _active;

  static int limit(JsonMap args, {int maximum = nativeMaxFileLimit}) {
    final raw = args['limit'] ?? min(nativeFileLimit, maximum);
    final value = raw is int ? raw : int.tryParse(raw.toString());
    if (value == null || value < 1 || value > maximum)
      throw const EngineException('文件大小上限无效');
    return value;
  }

  Future<File> privateFile(String path, {bool existing = true}) async {
    final handle = paths.handle(path);
    var current = paths.root;
    for (final part in handle.split('/')) {
      current = p.join(current, part);
      final type = await FileSystemEntity.type(current, followLinks: false);
      if (type == FileSystemEntityType.link)
        throw const EngineException('不支持符号链接文件');
    }
    final file = File(current);
    if (existing &&
        await FileSystemEntity.type(current, followLinks: false) !=
            FileSystemEntityType.file) {
      throw const EngineException('请选择已存在的普通文件');
    }
    return file;
  }

  Future<JsonMap> info(File file) async => {
    'path': paths.handle(file.path),
    'name': p.basename(file.path),
    'size': await file.length(),
  };

  Future<List<JsonMap>> list() async {
    final result = <JsonMap>[];
    await for (final entity in Directory(
      paths.root,
    ).list(recursive: true, followLinks: false)) {
      if (entity is File && !p.basename(entity.path).startsWith('.iotools-')) {
        if (result.length >= 10000)
          throw const EngineException('文件数量超过 10000，请先整理应用私有目录');
        result.add(await info(await privateFile(entity.path)));
      }
    }
    result.sort((a, b) => (a['path'] as String).compareTo(b['path'] as String));
    return result;
  }

  Future<String> read(JsonMap args) async {
    final maximum = limit(args, maximum: 8 * 1024 * 1024);
    final file = await privateFile(args['path']?.toString() ?? '');
    if (await file.length() > maximum)
      throw const EngineException('文本文件超过编辑上限');
    final bytes = BytesBuilder(copy: false);
    await for (final chunk in file.openRead()) {
      if (bytes.length + chunk.length > maximum)
        throw const EngineException('文本文件超过编辑上限');
      bytes.add(chunk);
    }
    return utf8.decode(bytes.takeBytes());
  }

  Future<void> cancel() async => _active?.cancel();

  Future<Object?> transfer(String method, JsonMap args) async {
    if (_active != null) throw const EngineException('已有文件操作正在进行');
    final maximum = limit(args);
    final token =
        '${DateTime.now().microsecondsSinceEpoch}-${Random.secure().nextInt(1 << 30)}';
    final task = _Transfer(File(p.join(paths.root, '.iotools-$token.cancel')));
    _active = task;
    try {
      if (method == 'files.export') return await _export(args, maximum, task);
      final picked = await dialogs.pick(bundle: method == 'files.importBundle');
      task.check();
      if (picked == null) return null;
      final declared = await picked.length();
      if (declared > maximum) throw const EngineException('所选文件超过允许大小');
      final folder = await Directory(
        paths.root,
      ).createTemp(method == 'files.importBundle' ? 'bundle-' : 'attachment-');
      var complete = false;
      try {
        final imported = File(
          p.join(
            folder.path,
            method == 'files.importBundle'
                ? '.source.zip'
                : safeFilename(picked.name),
          ),
        );
        await _copy(picked.openRead(), imported, maximum, declared, task);
        if (method == 'files.importBundle') {
          final result = await Isolate.run(
            () => extractPrivateBundle(
              imported.path,
              folder.path,
              maximum,
              task.marker.path,
            ),
          );
          task.check();
          await imported.delete();
          final files = <JsonMap>[];
          for (final path in (result['paths'] as List).cast<String>()) {
            files.add(await info(File(path)));
          }
          complete = true;
          return {
            'directory': paths.handle(folder.path),
            'files': files,
            'bytes': result['bytes'],
          };
        }
        task.check();
        complete = true;
        return await info(imported);
      } finally {
        if (!complete && await folder.exists())
          await folder.delete(recursive: true);
      }
    } finally {
      _active = null;
      await task.cancellation;
      if (await task.marker.exists()) await task.marker.delete();
    }
  }

  Future<Object?> _export(JsonMap args, int maximum, _Transfer task) async {
    final Stream<List<int>> input;
    final int size;
    if (args['path'] != null) {
      final file = await privateFile(args['path'].toString());
      size = await file.length();
      input = file.openRead();
    } else if (args['text'] is String) {
      final bytes = utf8.encode(args['text'] as String);
      size = bytes.length;
      input = Stream.value(bytes);
    } else {
      throw const EngineException('没有待导出内容');
    }
    if (size > maximum) throw const EngineException('内容超过导出上限');
    final destination = await dialogs.save(
      safeFilename(args['name']?.toString() ?? 'iotools-export.txt'),
    );
    task.check();
    if (destination == null) return null;
    await _copy(input, File(destination), maximum, size, task);
    return {'exported': true};
  }
}

String safeFilename(String name) {
  var value = name.replaceAll(RegExp(r'[\\/:<>"|?*\x00-\x1f\x7f]'), '_').trim();
  while (value.endsWith('.')) {
    value = value.substring(0, value.length - 1);
  }
  if (value.isEmpty || value == '.' || value == '..') value = 'data.bin';
  if (RegExp(
    r'^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)',
    caseSensitive: false,
  ).hasMatch(value))
    value = '_$value';
  final runes = value.runes.toList();
  while (utf8.encode(String.fromCharCodes(runes)).length > 120) {
    runes.removeLast();
  }
  return String.fromCharCodes(runes);
}

Future<void> _copy(
  Stream<List<int>> input,
  File output,
  int maximum,
  int declared,
  _Transfer task,
) async {
  task.check();
  await output.create(exclusive: true);
  var complete = false;
  RandomAccessFile? target;
  try {
    target = await output.open(mode: FileMode.writeOnly);
    var count = 0;
    await for (final chunk in input) {
      task.check();
      count += chunk.length;
      if (count > maximum) throw const EngineException('文件超过允许大小');
      await target.writeFrom(chunk);
    }
    task.check();
    if (declared >= 0 && count != declared)
      throw const EngineException('文件声明大小与实际内容不一致');
    await target.flush();
    complete = true;
  } finally {
    await target?.close();
    if (!complete && await output.exists()) await output.delete();
  }
}

/// Runs in a worker isolate; the cancellation marker is observed while writing
/// output chunks, so decompression cannot block Flutter or grow past its limit.
JsonMap extractPrivateBundle(
  String source,
  String directory,
  int maximum,
  String cancelPath,
) {
  final paths = PrivatePaths(directory);
  final input = InputFileStream(source);
  final cancel = File(cancelPath);
  void check() {
    if (cancel.existsSync()) throw const EngineException('文件操作已取消');
  }

  final written = <String>[];
  var total = 0;
  try {
    check();
    _checkZipDirectoryBounds(source);
    final zip = ZipDirectory()..read(input);
    if (zip.fileHeaders.isEmpty || zip.fileHeaders.length > 256)
      throw const EngineException('ZIP 必须包含 1–256 个项目');
    final names = <String>{};
    // Validate every name and size before any entry is decompressed. Reading
    // ZipDirectory also retains duplicate entries which ZipDecoder merges.
    for (final header in zip.fileHeaders) {
      check();
      final raw = header.filename.endsWith('/')
          ? header.filename.substring(0, header.filename.length - 1)
          : header.filename;
      if (raw.startsWith('/') || raw.startsWith('\\') || raw.contains(':'))
        throw const EngineException('ZIP 不支持绝对路径');
      final name = paths.handle(raw);
      if (!names.add(name.toLowerCase()))
        throw const EngineException('ZIP 存在重复或大小写冲突路径');
      final kind = (header.externalFileAttributes >> 16) & 0xf000;
      if (kind != 0 && kind != 0x8000 && kind != 0x4000)
        throw const EngineException('ZIP 不支持链接或特殊设备');
      if ((header.generalPurposeBitFlag & 1) != 0)
        throw const EngineException('不支持加密 ZIP');
      if (header.compressionMethod != 0 && header.compressionMethod != 8)
        throw const EngineException('ZIP 仅支持 store / deflate 压缩');
      total += header.uncompressedSize;
      if (header.uncompressedSize < 0 || total > maximum)
        throw const EngineException('ZIP 解压总大小超过上限');
    }
    total = 0;
    for (final header in zip.fileHeaders) {
      check();
      final raw = header.filename.endsWith('/')
          ? header.filename.substring(0, header.filename.length - 1)
          : header.filename;
      final target = paths.resolve(raw);
      if (header.filename.endsWith('/')) {
        Directory(target).createSync(recursive: true);
        continue;
      }
      Directory(p.dirname(target)).createSync(recursive: true);
      File(target).createSync(exclusive: true);
      final output = _BoundedZipOutput(
        OutputFileStream(target, bufferSize: 64 * 1024),
        maximum - total,
        check,
      );
      try {
        header.file!.decompress(output);
        if (output.length != header.uncompressedSize ||
            output.crc != header.crc32)
          throw const EngineException('ZIP 条目大小或校验和无效');
        total += output.length;
      } finally {
        output.closeSync();
      }
      written.add(target);
    }
    check();
    if (written.isEmpty) throw const EngineException('ZIP 没有可导入文件');
    return {'paths': written, 'bytes': total};
  } finally {
    input.closeSync();
  }
}

// Validate the small directory record before the archive package materializes
// its header list. ZIP64 is supported, but a claimed million-entry directory
// must not allocate memory before the application's 256-entry limit is checked.
void _checkZipDirectoryBounds(String source) {
  final input = File(source).openSync();
  try {
    final size = input.lengthSync();
    final start = max(0, size - 65557);
    input.setPositionSync(start);
    final tail = input.readSync(size - start);
    var found = -1;
    for (var i = tail.length - 22; i >= 0; i--) {
      if (tail[i] == 0x50 &&
          tail[i + 1] == 0x4b &&
          tail[i + 2] == 0x05 &&
          tail[i + 3] == 0x06) {
        final b = ByteData.sublistView(tail, i);
        if (i + 22 + b.getUint16(20, Endian.little) == tail.length) {
          found = i;
          break;
        }
      }
    }
    if (found < 0) throw const EngineException('ZIP 目录结构无效');
    final record = ByteData.sublistView(tail, found);
    if (record.getUint16(4, Endian.little) != 0 ||
        record.getUint16(6, Endian.little) != 0)
      throw const EngineException('不支持分卷 ZIP');
    var entries = record.getUint16(10, Endian.little);
    var bytes = record.getUint32(12, Endian.little);
    var offset = record.getUint32(16, Endian.little);
    if (entries == 65535 || bytes == 0xffffffff || offset == 0xffffffff) {
      final locatorOffset = start + found - 20;
      if (locatorOffset < 0) throw const EngineException('ZIP64 目录结构无效');
      input.setPositionSync(locatorOffset);
      final locatorBytes = input.readSync(20);
      if (locatorBytes.length != 20)
        throw const EngineException('ZIP64 目录结构无效');
      final locator = ByteData.sublistView(locatorBytes);
      if (locator.getUint32(0, Endian.little) != 0x07064b50 ||
          locator.getUint32(4, Endian.little) != 0 ||
          locator.getUint32(16, Endian.little) != 1)
        throw const EngineException('不支持分卷 ZIP64');
      final position = locator.getUint64(8, Endian.little);
      if (position < 0 || position > size - 56)
        throw const EngineException('ZIP64 目录偏移无效');
      input.setPositionSync(position);
      final headerBytes = input.readSync(56);
      if (headerBytes.length != 56) throw const EngineException('ZIP64 目录结构无效');
      final header = ByteData.sublistView(headerBytes);
      if (header.getUint32(0, Endian.little) != 0x06064b50 ||
          header.getUint32(16, Endian.little) != 0 ||
          header.getUint32(20, Endian.little) != 0)
        throw const EngineException('ZIP64 目录结构无效');
      entries = header.getUint64(32, Endian.little);
      bytes = header.getUint64(40, Endian.little);
      offset = header.getUint64(48, Endian.little);
    }
    if (entries < 1 ||
        entries > 256 ||
        bytes < 0 ||
        bytes > 4 * 1024 * 1024 ||
        offset < 0 ||
        offset + bytes > size)
      throw const EngineException('ZIP 目录数量、大小或范围超过上限');
    input.setPositionSync(offset);
    final directory = input.readSync(bytes);
    if (directory.length != bytes) throw const EngineException('ZIP 目录不完整');
    final index = ByteData.sublistView(directory);
    var cursor = 0, actualEntries = 0;
    while (cursor < directory.length) {
      if (directory.length - cursor < 46 ||
          index.getUint32(cursor, Endian.little) != 0x02014b50)
        throw const EngineException('ZIP 目录条目无效');
      final nameLength = index.getUint16(cursor + 28, Endian.little);
      if (nameLength < 1 || nameLength > 1024 || ++actualEntries > 256)
        throw const EngineException('ZIP 名称或条目数量超过上限');
      cursor +=
          46 +
          nameLength +
          index.getUint16(cursor + 30, Endian.little) +
          index.getUint16(cursor + 32, Endian.little);
      if (cursor > directory.length) throw const EngineException('ZIP 目录条目不完整');
    }
    if (actualEntries != entries) throw const EngineException('ZIP 声明条目数量不一致');
  } finally {
    input.closeSync();
  }
}

class _BoundedZipOutput extends OutputStream {
  _BoundedZipOutput(this.target, this.maximum, this.check)
    : super(byteOrder: ByteOrder.littleEndian);
  final OutputFileStream target;
  final int maximum;
  final void Function() check;
  int crc = 0;
  @override
  int get length => target.length;
  void reserve(int count) {
    check();
    if (count < 0 || length + count > maximum)
      throw const EngineException('ZIP 实际解压大小超过上限');
  }

  @override
  void writeByte(int value) => writeBytes([value]);
  @override
  void writeBytes(List<int> bytes, {int? length}) {
    final count = length ?? bytes.length;
    reserve(count);
    final chunk = count == bytes.length ? bytes : bytes.sublist(0, count);
    crc = getCrc32(chunk, crc);
    target.writeBytes(chunk);
  }

  @override
  void writeStream(InputStream stream) {
    while (!stream.isEOS) {
      writeBytes(stream.readBytes(min(65536, stream.length)).toUint8List());
    }
  }

  @override
  Uint8List subset(int start, [int? end]) => target.subset(start, end);
  @override
  void flush() => target.flush();
  @override
  void clear() => throw UnsupportedError('bounded output cannot be cleared');
  @override
  void closeSync() => target.closeSync();
}
