import 'package:flutter/material.dart';
import 'app/app.dart';
import 'core/runtime.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final runtime = createRuntime();
  runApp(IotoolsApp(engine: runtime.engine, platform: runtime.platform));
}
