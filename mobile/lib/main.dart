import 'package:flutter/material.dart';
import 'app/app.dart';
import 'core/runtime.dart';
import 'core/offline_fonts.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await loadOfflineFonts();
  final runtime = createRuntime();
  runApp(IotoolsApp(engine: runtime.engine, platform: runtime.platform));
}
