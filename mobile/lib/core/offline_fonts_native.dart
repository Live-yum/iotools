import 'dart:io';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

/// Desktop installations do not all provide usable CJK system fallbacks.
/// Ship the same pinned fonts as the offline Web gateway on every desktop.
Future<void> loadOfflineFonts() async {
  if (!Platform.isLinux && !Platform.isWindows && !Platform.isMacOS) return;
  final executableDirectory = p.dirname(Platform.resolvedExecutable);
  final folder = Platform.isMacOS
      ? p.normalize(p.join(executableDirectory, '..', 'Resources', 'iotools-fonts'))
      : p.join(executableDirectory, 'data', 'iotools-fonts');
  await Future.wait([
    for (final name in ['NotoSansSC', 'NotoEmoji'])
      (FontLoader(name)..addFont(File(p.join(folder, '$name.ttf')).readAsBytes().then(ByteData.sublistView))).load(),
  ]);
}
