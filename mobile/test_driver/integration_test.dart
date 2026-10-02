import 'dart:io';
import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  await integrationDriver(
    onScreenshot: (name, bytes, [args]) async {
      final root = Directory('build/flutter-evidence')
        ..createSync(recursive: true);
      final safe = name.replaceAll(RegExp(r'[^a-zA-Z0-9_-]'), '_');
      await File('${root.path}/$safe.png').writeAsBytes(bytes);
      return true;
    },
  );
}
