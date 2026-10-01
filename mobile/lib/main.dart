import 'package:flutter/material.dart';
import 'app/app.dart';
import 'core/engine.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(
    const IotoolsApp(
      engine: MethodChannelEngine(),
      platform: MethodChannelPlatform(),
    ),
  );
}
