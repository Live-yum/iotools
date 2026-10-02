"""Hermetic production bundle rejection cases; actual archived builds are also checked in CI."""
import json
from pathlib import Path
import plistlib
import struct
import tempfile
import unittest

from ios_release_gate import macho_dependencies, verify_production_bundle


def macho(links=(), payload=b'', platform=2):
    commands = struct.pack('<IIIIII', 0x32, 24, platform, 0xF0000, 0x120500, 0)
    for link in links:
        name = link.encode() + b'\0'
        size = (24 + len(name) + 7) // 8 * 8
        commands += struct.pack('<IIIIII', 0xc, size, 24, 0, 0, 0) + name + b'\0' * (size - 24 - len(name))
    return struct.pack('<IIIIIIII', 0xfeedfacf, 0x100000c, 0, 2, len(links) + 1, len(commands), 0, 0) + commands + payload


class ProductionGateTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.app = self.root / 'Runner.app'
        self.app.mkdir()
        self.registrant = self.root / 'GeneratedPluginRegistrant.m'
        self.registrant.write_text('[FileSelectorPlugin registerWithRegistrar:selector]; [PathProviderPlugin registerWithRegistrar:path];')
        self.plugins = self.root / 'plugins.json'
        self.plugins.write_text(json.dumps({'plugins': {'ios': [
            {'name': 'file_selector_ios', 'dev_dependency': False},
            {'name': 'path_provider_foundation', 'dev_dependency': False}]}}))
        (self.app / 'Info.plist').write_bytes(plistlib.dumps({'CFBundleExecutable': 'Runner', 'CFBundleSupportedPlatforms': ['iPhoneOS']}))
        self.links = ['@rpath/file_selector_ios.framework/file_selector_ios', '@rpath/path_provider_foundation.framework/path_provider_foundation']
        (self.app / 'Runner').write_bytes(macho(self.links))
        for name in ('App', 'Flutter', 'file_selector_ios', 'path_provider_foundation'):
            file = self.app / f'Frameworks/{name}.framework/{name}'
            file.parent.mkdir(parents=True)
            file.write_bytes(macho())

    def verify(self):
        return verify_production_bundle(self.app, self.registrant, self.plugins)

    def test_clean_bundle_keeps_essential_plugins(self):
        self.assertEqual(self.verify()['status'], 'passed')

    def test_active_registration_rejected(self):
        self.registrant.write_text(self.registrant.read_text() + ' IntegrationTestPlugin')
        with self.assertRaisesRegex(RuntimeError, 'registrant'):
            self.verify()

    def test_essential_class_names_without_registration_rejected(self):
        self.registrant.write_text('// FileSelectorPlugin PathProviderPlugin')
        with self.assertRaisesRegex(RuntimeError, 'Essential production registrar'):
            self.verify()

    def test_framework_path_rejected_even_without_registration(self):
        (self.app / 'Frameworks/integration_test.framework').mkdir()
        with self.assertRaisesRegex(RuntimeError, 'Test-only file'):
            self.verify()

    def test_linked_test_library_rejected(self):
        (self.app / 'Runner').write_bytes(macho(self.links + ['@rpath/integration_test.framework/integration_test']))
        with self.assertRaisesRegex(RuntimeError, 'symbol/channel'):
            self.verify()

    def test_embedded_test_channel_rejected(self):
        (self.app / 'Frameworks/App.framework/App').write_bytes(macho(payload=b'plugins.flutter.io/integration_test'))
        with self.assertRaisesRegex(RuntimeError, 'symbol/channel'):
            self.verify()

    def test_unknown_dev_plugin_rejected(self):
        value = json.loads(self.plugins.read_text())
        value['plugins']['ios'].append({'name': 'new_test_plugin', 'dev_dependency': True})
        self.plugins.write_text(json.dumps(value))
        with self.assertRaisesRegex(RuntimeError, 'Dev-only'):
            self.verify()

    def test_missing_essential_plugin_rejected(self):
        (self.app / 'Frameworks/file_selector_ios.framework/file_selector_ios').unlink()
        with self.assertRaisesRegex(RuntimeError, 'Essential production Mach-O'):
            self.verify()

    def test_missing_linkage_rejected(self):
        (self.app / 'Runner').write_bytes(macho())
        with self.assertRaisesRegex(RuntimeError, 'linkage'):
            self.verify()

    def test_debug_discovery_rejected(self):
        p = self.app / 'Info.plist'
        info = plistlib.loads(p.read_bytes())
        info['NSBonjourServices'] = ['_dartVmService._tcp']
        p.write_bytes(plistlib.dumps(info))
        with self.assertRaisesRegex(RuntimeError, 'Debug VM'):
            self.verify()

    def test_corrupt_macho_rejected(self):
        with self.assertRaisesRegex(RuntimeError, 'extent'):
            macho_dependencies(macho(self.links)[:40])

    def test_flutter_one_slice_fat_container_accepted(self):
        data = macho(self.links)
        fat = struct.pack('>IIIIIII', 0xcafebabe, 1, 0x100000c, 0, 28, len(data), 0) + data
        self.assertEqual(macho_dependencies(fat), self.links)

    def test_extra_macho_slices_rejected(self):
        with self.assertRaisesRegex(RuntimeError, 'exactly one'):
            macho_dependencies(struct.pack('>II', 0xcafebabe, 2))

    def test_simulator_framework_in_device_bundle_rejected(self):
        (self.app / 'Frameworks/App.framework/App').write_bytes(macho(platform=7))
        with self.assertRaisesRegex(RuntimeError, 'must target IOS'):
            self.verify()


if __name__ == '__main__':
    unittest.main()
