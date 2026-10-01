import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'platform icons preserve the approved avatar with safe padding',
    () async {
      final codec = await ui.instantiateImageCodec(
        await File(
          'android/app/src/main/res/drawable-nodpi/iotools_avatar.png',
        ).readAsBytes(),
      );
      final original = (await codec.getNextFrame()).image;
      expect(original.width, 512);
      expect(original.height, 512);
      Future<Uint8List> render(int size) async {
        final record = ui.PictureRecorder();
        final canvas = ui.Canvas(record);
        canvas.drawColor(const ui.Color(0xff08141f), ui.BlendMode.src);
        canvas.drawImageRect(
          original,
          const ui.Rect.fromLTWH(0, 0, 512, 512),
          ui.Rect.fromLTWH(size * .125, size * .125, size * .75, size * .75),
          ui.Paint()..filterQuality = ui.FilterQuality.high,
        );
        final picture = record.endRecording();
        final image = await picture.toImage(size, size);
        final data = await image.toByteData(format: ui.ImageByteFormat.png);
        image.dispose();
        picture.dispose();
        return data!.buffer.asUint8List();
      }

      final sample = await render(256);
      expect(sample.length, greaterThan(1000));
      if (const bool.fromEnvironment('IOTOOLS_GENERATE_PLATFORM_ICONS')) {
        for (final path in [
          'macos/Runner/Assets.xcassets/AppIcon.appiconset',
          'ios/Runner/Assets.xcassets/AppIcon.appiconset',
        ]) {
          final index =
              jsonDecode(await File('$path/Contents.json').readAsString())
                  as Map;
          for (final raw in index['images'] as List) {
            final item = raw as Map;
            if (item['filename'] == null) continue;
            final points = double.parse(
              (item['size'] as String).split('x').first,
            );
            final scale = double.parse(
              (item['scale'] as String).replaceAll('x', ''),
            );
            await File(
              '$path/${item['filename']}',
            ).writeAsBytes(await render((points * scale).round()));
          }
        }
        final sizes = [16, 32, 48, 256], pngs = <Uint8List>[];
        for (final size in sizes) pngs.add(await render(size));
        final table = ByteData(6 + 16 * sizes.length)
          ..setUint16(2, 1, Endian.little)
          ..setUint16(4, sizes.length, Endian.little);
        var offset = table.lengthInBytes;
        for (var i = 0; i < sizes.length; i++) {
          final pos = 6 + 16 * i, size = sizes[i] == 256 ? 0 : sizes[i];
          table
            ..setUint8(pos, size)
            ..setUint8(pos + 1, size)
            ..setUint16(pos + 4, 1, Endian.little)
            ..setUint16(pos + 6, 32, Endian.little)
            ..setUint32(pos + 8, pngs[i].length, Endian.little)
            ..setUint32(pos + 12, offset, Endian.little);
          offset += pngs[i].length;
        }
        final ico = BytesBuilder()..add(table.buffer.asUint8List());
        for (final png in pngs) ico.add(png);
        await File(
          'windows/runner/resources/app_icon.ico',
        ).writeAsBytes(ico.takeBytes());
        await File('linux/iotools.png').writeAsBytes(await render(512));
        await File('web/favicon.png').writeAsBytes(await render(32));
        for (final size in [192, 512])
          for (final suffix in ['', '-maskable'])
            await File(
              'web/icons/Icon$suffix-$size.png',
            ).writeAsBytes(await render(size));
      }
      original.dispose();
      codec.dispose();
    },
  );
}
