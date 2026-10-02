import 'dart:convert';
import 'dart:io';
import 'package:integration_test/integration_test_driver.dart';

Future<void> main() async {
  await integrationDriver(responseDataCallback: (data) async {
    if (data == null) throw StateError('Missing native integration evidence');
    final screenshots = data['nativeScreenshots'] as Map?;
    if (screenshots == null || screenshots.length < 4) {
      throw StateError('Missing sandboxed macOS UI screenshots');
    }
    final folder = Directory('build/flutter-evidence')..createSync(recursive: true);
    for (final entry in screenshots.entries) {
      final name = entry.key.toString();
      if (!RegExp(r'^[a-zA-Z0-9_-]+$').hasMatch(name)) {
        throw StateError('Invalid evidence name');
      }
      await File('${folder.path}/native-$name.png')
          .writeAsBytes(base64Decode(entry.value as String));
    }
    await File('${folder.path}/macos-native-result.json').writeAsString(
      jsonEncode({'status': 'passed', 'screenshots': screenshots.keys.toList(),
        'scope': 'actual sandboxed Flutter UI and Go HTTP: exact write, cancel, hide/no replay'}),
    );
  });
}
