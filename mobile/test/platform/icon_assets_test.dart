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
      Future<Uint8List> render(int size, {bool transparent = false}) async {
        final record = ui.PictureRecorder();
        final canvas = ui.Canvas(record);
        // Windows ICO supports alpha. Keep the avatar's alpha instead of
        // flattening it onto the opaque background required by iOS icons.
        canvas.drawColor(
          transparent ? const ui.Color(0x00000000) : const ui.Color(0xff08141f),
          ui.BlendMode.src,
        );
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

      Future<void> checkPng(Uint8List png, int size, {required bool transparent})
          async {
        final decoded = await ui.instantiateImageCodec(png);
        final image = (await decoded.getNextFrame()).image;
        try {
          expect(image.width, size);
          expect(image.height, size);
          final rgba = (await image.toByteData(
            format: ui.ImageByteFormat.rawRgba,
          ))!;
          int channel(int x, int y, int c) =>
              rgba.getUint8((y * size + x) * 4 + c);
          for (final x in [0, size - 1]) {
            for (final y in [0, size - 1]) {
              expect(channel(x, y, 3), transparent ? 0 : 255,
                  reason: '$size px icon corner alpha');
            }
          }
          // This is inside the original black beret, not its surroundings.
          // Transparency must never come from removing all dark pixels.
          final hatX = (size * .5).floor();
          final hatY = (size * (.125 + .75 * 96 / 512)).floor();
          expect(channel(hatX, hatY, 3), 255,
              reason: '$size px beret stays opaque');
          for (var c = 0; c < 3; c++) {
            expect(channel(hatX, hatY, c), lessThan(80),
                reason: '$size px beret stays dark');
          }
          if (transparent) {
            var clear = 0;
            var opaque = 0;
            for (var i = 3; i < rgba.lengthInBytes; i += 4) {
              if (rgba.getUint8(i) == 0) clear++;
              if (rgba.getUint8(i) == 255) opaque++;
            }
            expect(clear, greaterThan(size * size ~/ 4));
            expect(opaque, greaterThan(0));
          }
        } finally {
          image.dispose();
          decoded.dispose();
        }
      }

      final sample = await render(256);
      expect(sample.length, greaterThan(1000));
      await checkPng(sample, 256, transparent: false);
      final sizes = [16, 32, 48, 256], pngs = <Uint8List>[];
      for (final size in sizes) {
        final png = await render(size, transparent: true);
        await checkPng(png, size, transparent: true);
        pngs.add(png);
      }
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
