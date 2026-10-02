import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:iotools_mobile/core/engine.dart';
import 'package:iotools_mobile/core/platform/native/private_paths.dart';

void main() {
  test(
    'Windows drive paths become portable handles without losing Unicode',
    () {
      final paths = PrivatePaths(
        r'C:\Users\测试\AppData\iotools',
        context: p.Context(style: p.Style.windows),
      );
      expect(
        paths.handle(r'C:\Users\测试\AppData\iotools\附件\配置😀.yaml'),
        '附件/配置😀.yaml',
      );
      expect(
        paths.resolve('附件/配置😀.yaml'),
        r'C:\Users\测试\AppData\iotools\附件\配置😀.yaml',
      );
      expect(paths.handle(r'c:\users\测试\appdata\iotools\a.yaml'), 'a.yaml');
      for (final value in [
        r'D:\iotools\a.yaml',
        r'C:\Users\测试\AppData\iotools-other\a.yaml',
        r'C:secret.yaml',
        r'folder\file.yaml',
        '../secret',
        'a//b',
        'a/./b',
        'a/../b',
        'CON.txt',
        'a/NUL',
        'a.',
        'file://a',
      ]) {
        expect(
          () => paths.handle(value),
          throwsA(isA<EngineException>()),
          reason: value,
        );
      }
    },
  );
  test('UNC roots are scoped to exact share and directory', () {
    final paths = PrivatePaths(
      r'\\server\share\iotools',
      context: p.Context(style: p.Style.windows),
    );
    expect(paths.handle(r'\\server\share\iotools\config.yaml'), 'config.yaml');
    expect(
      () => paths.handle(r'\\server\other\iotools\config.yaml'),
      throwsA(isA<EngineException>()),
    );
  });
  test('POSIX roots reject escapes and require a child file', () {
    final paths = PrivatePaths(
      '/private/iotools',
      context: p.Context(style: p.Style.posix),
    );
    expect(
      paths.handle('/private/iotools/bundle/config.yaml'),
      'bundle/config.yaml',
    );
    for (final value in [
      '/private/iotools',
      '/private/iotools2/a',
      '/private/iotools/a/../b',
      '/etc/passwd',
      'a\u0000b',
    ]) {
      expect(() => paths.handle(value), throwsA(isA<EngineException>()));
    }
  });
}
