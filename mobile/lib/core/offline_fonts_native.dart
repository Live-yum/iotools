import 'dart:io';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

/// Linux distributions and Flutter ARM64 engines do not all provide a usable
/// CJK system fallback. Ship the same pinned fonts as the offline Web gateway.
Future<void> loadOfflineFonts() async {
  if (!Platform.isLinux) return;
  final folder = p.join(p.dirname(Platform.resolvedExecutable), 'data', 'iotools-fonts');
  await Future.wait([
    for (final name in ['NotoSansSC', 'NotoEmoji'])
      (FontLoader(name)..addFont(File(p.join(folder, '$name.ttf')).readAsBytes().then(ByteData.sublistView))).load(),
  ]);
}
