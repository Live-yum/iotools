import 'package:path/path.dart' as p;
import '../../engine.dart';

/// Portable handles are root-relative POSIX paths. OS paths never enter YAML
/// merely because a system picker happened to return a Windows drive or UNC.
class PrivatePaths {
  PrivatePaths(String root, {p.Context? context})
    : context = context ?? p.context,
      root = (context ?? p.context).normalize(root) {
    if (!this.context.isAbsolute(this.root)) {
      throw const EngineException('应用数据目录必须是绝对路径');
    }
  }
  final p.Context context;
  final String root;

  String handle(String value) {
    if (value.isEmpty || RegExp(r'[\x00-\x1f\x7f]').hasMatch(value)) {
      throw const EngineException('文件路径为空或包含控制字符');
    }
    var relative = value;
    if (context.isAbsolute(value)) {
      // Reject traversal before normalize: a canonical result alone is not a
      // reason to accept an unreviewed alternate spelling.
      if (context.split(value).any((v) => v == '.' || v == '..') ||
          !context.isWithin(root, value)) {
        throw const EngineException('文件路径不在应用私有目录内');
      }
      relative = context.relative(value, from: root);
      if (context.style == p.Style.windows)
        relative = relative.replaceAll('\\', '/');
    }
    if (relative.contains('\\') || relative.contains(':')) {
      throw const EngineException('请使用应用私有目录内的相对路径');
    }
    final pieces = relative.split('/');
    if (pieces.length > 32 ||
        pieces.any((v) => v.isEmpty || v == '.' || v == '..')) {
      throw const EngineException('文件路径包含空段、目录穿越或过多层级');
    }
    // These are ambiguous/reserved on Windows, including when a ZIP was
    // created on another OS and is later shared between desktop platforms.
    for (final part in pieces) {
      if (part.endsWith('.') ||
          part.endsWith(' ') ||
          RegExp(r'[<>"|?*]').hasMatch(part) ||
          RegExp(
            r'^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)',
            caseSensitive: false,
          ).hasMatch(part)) {
        throw const EngineException('文件名包含跨平台不支持的字符或保留名称');
      }
    }
    return pieces.join('/');
  }

  String resolve(String value) =>
      context.joinAll([root, ...handle(value).split('/')]);
}
