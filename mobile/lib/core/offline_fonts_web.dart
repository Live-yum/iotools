import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;

/// These fonts ship inside the same local gateway as CanvasKit. The browser
/// must not disclose text by requesting remote Google fallback font subsets.
Future<void> loadOfflineFonts() async {
  final client = http.Client();
  Future<ByteData> bytes(String name) async {
    final response = await client.get(Uri.base.resolve('/fonts/$name.ttf'));
    if (response.statusCode != 200 || response.bodyBytes.length > 24 * 1024 * 1024) {
      throw StateError('完整 Web 包中缺少本机字体 $name');
    }
    return ByteData.sublistView(response.bodyBytes);
  }
  try {
    await Future.wait([
      for (final name in ['NotoSansSC', 'NotoEmoji'])
        (FontLoader(name)..addFont(bytes(name))).load(),
    ]);
  } finally {
    client.close();
  }
}
