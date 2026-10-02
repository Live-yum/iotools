"""Reject test-only plugins in actual iOS production bundles, without changing them."""
import hashlib
import json
from pathlib import Path
import plistlib
import struct

FORBIDDEN = (b'integration_test', b'IntegrationTestPlugin', b'XCTest', b'XCUI',
             b'flutter_driver', b'FlutterDriver', b'ext.flutter.driver',
             b'plugins.flutter.io/integration_test')
REQUIRED_PLUGINS = {'file_selector_ios', 'path_provider_foundation'}
REQUIRED_REGISTRARS = ('FileSelectorPlugin', 'PathProviderPlugin')
MACHO_MAGICS = (b'\xcf\xfa\xed\xfe', b'\xce\xfa\xed\xfe',
                b'\xca\xfe\xba\xbe', b'\xca\xfe\xba\xbf')


def require(value, message):
    if not value:
        raise RuntimeError(message)


def macho_dependencies(data):
    """Parse one arm64 Mach-O slice; Flutter may wrap that single slice in FAT."""
    if data[:4] in MACHO_MAGICS[2:]:
        require(len(data) >= 8 and struct.unpack_from('>I', data, 4)[0] == 1,
                'Expected exactly one arm64 Mach-O slice')
        wide = data[:4] == MACHO_MAGICS[3]
        require(len(data) >= (40 if wide else 28), 'Truncated FAT Mach-O header')
        if wide:
            cpu, _, start, length, _, _ = struct.unpack_from('>IIQQII', data, 8)
        else:
            cpu, _, start, length, _ = struct.unpack_from('>IIIII', data, 8)
        require(cpu == 0x100000c and start >= (40 if wide else 28)
                and start + length <= len(data), 'Invalid arm64 FAT Mach-O slice')
        data = data[start:start + length]
    require(len(data) >= 32 and data[:4] == MACHO_MAGICS[0], 'Expected a thin 64-bit Mach-O')
    require(struct.unpack_from('<I', data, 4)[0] == 0x100000c, 'Expected arm64 Mach-O')
    count, length = struct.unpack_from('<II', data, 16)
    require(32 + length <= len(data) and count <= length // 8, 'Invalid Mach-O command extent')
    offset, dependencies, platforms = 32, [], []
    for _ in range(count):
        require(offset + 8 <= 32 + length, 'Truncated Mach-O command')
        command, size = struct.unpack_from('<II', data, offset)
        require(size >= 8 and offset + size <= 32 + length, 'Invalid Mach-O command size')
        if command in (0xc, 0x80000018, 0x8000001f, 0x80000023):
            require(size >= 24, 'Truncated dylib command')
            start = struct.unpack_from('<I', data, offset + 8)[0]
            require(24 <= start < size, 'Invalid dylib name offset')
            name = data[offset + start:offset + size]
            require(b'\0' in name, 'Unterminated dylib name')
            dependencies.append(name.split(b'\0', 1)[0].decode('utf-8'))
        if command == 0x32:
            require(size >= 24, 'Truncated Mach-O build version')
            platforms.append(struct.unpack_from('<I', data, offset + 8)[0])
        offset += size
    require(offset == 32 + length, 'Mach-O command length mismatch')
    require(platforms == [2], 'Every production Mach-O must target IOS, not simulator or macOS')
    return dependencies


def verify_production_bundle(app, registrant, plugin_metadata):
    app, registrant, plugin_metadata = map(Path, (app, registrant, plugin_metadata))
    registration = registrant.read_bytes()
    require(not any(token in registration for token in FORBIDDEN), 'Test plugin in generated registrant')
    require(all(f'[{token} registerWithRegistrar:'.encode() in registration for token in REQUIRED_REGISTRARS),
            'Essential production registrar missing')
    metadata = json.loads(plugin_metadata.read_text())
    plugins = metadata['plugins']['ios']
    require(REQUIRED_PLUGINS <= {p['name'] for p in plugins}, 'Essential production plugin missing')
    require(not any(p.get('dev_dependency') for p in plugins), 'Dev-only iOS plugin remains in metadata')
    require(not any(token in json.dumps(plugins).encode() for token in FORBIDDEN), 'Test plugin in iOS metadata')
    info = plistlib.loads((app / 'Info.plist').read_bytes())
    require(info.get('CFBundleSupportedPlatforms') == ['iPhoneOS'], 'Expected production iPhoneOS bundle')
    require(not any('dart' in service.lower() or 'flutter' in service.lower()
                    for service in info.get('NSBonjourServices', [])), 'Debug VM discovery in production Info.plist')
    require(not (app / 'Frameworks/App.framework/flutter_assets/kernel_blob.bin').exists(),
            'Debug Dart kernel in production bundle')
    binaries = {}
    for file in sorted(app.rglob('*')):
        relative = file.relative_to(app).as_posix()
        require(not any(token.lower() in relative.lower().encode() for token in FORBIDDEN),
                f'Test-only file in production app: {relative}')
        require(not file.is_symlink(), f'Unexpected iOS bundle symlink: {relative}')
        if not file.is_file():
            continue
        data = file.read_bytes()
        if data[:4] not in MACHO_MAGICS:
            continue
        require(not any(token in data for token in FORBIDDEN),
                f'Test-only symbol/channel in production Mach-O: {relative}')
        binaries[relative] = {'sha256': hashlib.sha256(data).hexdigest(),
                              'linked_libraries': macho_dependencies(data)}
    required = {info['CFBundleExecutable'], 'Frameworks/Flutter.framework/Flutter',
                'Frameworks/App.framework/App'} | {f'Frameworks/{name}.framework/{name}' for name in REQUIRED_PLUGINS}
    require(required <= binaries.keys(), 'Essential production Mach-O missing')
    links = binaries[info['CFBundleExecutable']]['linked_libraries']
    require(all(f'@rpath/{name}.framework/{name}' in links for name in REQUIRED_PLUGINS),
            'Essential production plugin linkage missing')
    return {'status': 'passed', 'scope': 'Actual app paths, all Mach-O strings/load commands, Info.plist, generated registrant and iOS plugin metadata',
            'registrant_sha256': hashlib.sha256(registration).hexdigest(),
            'plugin_metadata_sha256': hashlib.sha256(plugin_metadata.read_bytes()).hexdigest(),
            'production_plugins': sorted(p['name'] for p in plugins), 'binaries': binaries}
