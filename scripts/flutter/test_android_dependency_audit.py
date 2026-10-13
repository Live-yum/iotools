#!/usr/bin/env python3
import json
import re
import hashlib
import struct
from pathlib import Path
import tempfile
import unittest
import zipfile
import android_dependency_audit as audit

class DependencyAuditTests(unittest.TestCase):
    def setUp(self):
        self.baseline = audit.reviewed_baseline(json.loads(audit.BASELINE.read_text()),
                                               json.loads(audit.UPDATES.read_text()))
        self.baseline.pop('lock_entry_sha256')
        self.versions = dict(self.baseline['lock_versions'])

    def lock(self, versions=None):
        return 'packages:\n' + ''.join(f'  {name}:\n    dependency: transitive\n    version: "{version}"\n'
            for name, version in (versions or self.versions).items()) + 'sdks:\n  dart: ">=3.9.0"\n'

    def test_exact_reviewed_jni_closure(self):
        self.assertEqual(audit.audit_lock(self.lock(), self.baseline)['versions'], self.versions)

    def test_reviewed_sdk_update_matches_real_lock(self):
        original = json.loads(audit.BASELINE.read_text())
        updates = json.loads(audit.UPDATES.read_text())
        baseline = audit.reviewed_baseline(original, updates)
        text = (audit.ROOT / 'mobile/pubspec.lock').read_text()
        audit.audit_lock(text, baseline)
        self.assertEqual(original, json.loads(audit.BASELINE.read_text()))
        for name, change in updates['lock_updates'].items():
            changed = text.replace(f'    version: "{change["to"]}"',
                                   '    version: "999.0.0"', 1)
            with self.subTest(name=name), self.assertRaises(ValueError):
                audit.audit_lock(changed, baseline)
        bad = json.loads(json.dumps(updates))
        bad['lock_updates']['path_provider_android']['to'] = '2.3.2'
        with self.assertRaises(ValueError): audit.reviewed_baseline(original, bad)

    def test_reviewed_update_metadata_fails_closed(self):
        original = json.loads(audit.BASELINE.read_text())
        updates = json.loads(audit.UPDATES.read_text())
        for mode in ('baseline', 'from', 'hash', 'maven_identity', 'maven_prior', 'closure', 'migration', 'archive', 'ndk', 'schema'):
            bad = json.loads(json.dumps(updates))
            change = next(iter(bad['lock_updates'].values()))
            coordinate = next(iter(bad['maven_updates']))
            if mode == 'baseline': bad['baseline_source_commit'] = 'wrong'
            if mode == 'from': change['from'] = '0.0.0'
            if mode == 'hash': change['lock_entry_sha256'] = 'not-a-hash'
            if mode == 'maven_identity': bad['maven_updates'][coordinate] = 'other:artifact:1.0'
            if mode == 'maven_prior': bad['maven_updates']['unknown:artifact:1.0'] = 'unknown:artifact:2.0'
            if mode == 'closure': bad['android_implementation']['restored_closure'].append('unexpected')
            if mode == 'migration': bad['android_implementation']['version'] = '2.3.2'
            if mode == 'archive': bad['android_implementation']['archive_sha256'] = 'not-a-hash'
            if mode == 'ndk': bad['android_implementation']['ndk_version'] = '28.2.13676358'
            if mode == 'schema': bad['schema'] = 1
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                audit.reviewed_baseline(original, bad)

    def test_reviewed_maven_update_remains_exact(self):
        baseline = audit.reviewed_baseline(json.loads(audit.BASELINE.read_text()),
                                          json.loads(audit.UPDATES.read_text()))
        text = 'releaseRuntimeClasspath - Resolved\n' + ''.join(
            '+--- ' + coordinate + '\n' for coordinate in baseline['resolved_maven_coordinates']) + 'BUILD SUCCESSFUL'
        audit.audit_maven(text, baseline)
        for changed in (text.replace('io.flutter:', 'unknown.flutter:'),
                        text.replace('BUILD SUCCESSFUL', '+--- example:extra:1.0\nBUILD SUCCESSFUL')):
            with self.assertRaises(ValueError): audit.audit_maven(changed, baseline)

    def test_archive_and_unrelated_lock_integrity(self):
        text = self.lock().replace('  path_provider_android:\n    dependency: transitive\n',
            '  path_provider_android:\n    dependency: "direct main"\n'
            '    description:\n      name: path_provider_android\n'
            f'      sha256: {self.baseline["path_provider_android_archive_sha256"]}\n'
            '      url: "https://pub.dev"\n    source: hosted\n')
        baseline = dict(self.baseline, lock_entry_sha256={name: hashlib.sha256(body.encode()).hexdigest()
            for name, body in re.findall(r'^  (\w+):\n((?:    .*\n|\n)+)', text, re.M)})
        audit.audit_lock(text, baseline)
        for changed in (text.replace('pub.dev', 'example.com'),
                        text.replace(self.baseline['path_provider_android_archive_sha256'], '0' * 64),
                        text.replace('  http:\n    dependency: transitive', '  http:\n    dependency: "direct main"')):
            with self.assertRaises(ValueError): audit.audit_lock(changed, baseline)

    def test_unrelated_drift_rejected(self):
        for name, value in [('file_selector_android', '0.5.2+7'), ('path_provider', '2.2.0'),
                             ('path_provider_android', '2.2.23'), ('jni', '1.0.0'), ('new_package', '1.0.0')]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                audit.audit_lock(self.lock(dict(self.versions, **{name: value})), self.baseline)

    def test_other_removed_dependency_rejected(self):
        for name in ('http', *audit.JNI_CLOSURE):
            values = dict(self.versions)
            del values[name]
            with self.subTest(name=name), self.assertRaises(ValueError):
                audit.audit_lock(self.lock(values), self.baseline)

    def graph(self):
        return {'packages': [{'name': name, 'version': version, 'kind': 'transitive', 'dependencies':
                 sorted(audit.JNI_DEPENDENCIES.get(name, set()))}
                for name, version in self.versions.items()] + [{'name': 'iotools_mobile', 'version': '0.3.0', 'kind': 'root'}]}

    def test_graph_matches_lock(self):
        audit.audit_graph(self.graph(), self.versions)

    def test_graph_drift_and_duplicates_rejected(self):
        for mode in ('duplicate', 'version', 'dependency'):
            graph = self.graph()
            if mode == 'duplicate': graph['packages'].append(graph['packages'][0])
            if mode == 'version': graph['packages'][0]['version'] = '0.0.1'
            if mode == 'dependency':
                next(x for x in graph['packages'] if x['name'] == 'path_provider_android')['dependencies'].append('unexpected')
            with self.subTest(mode=mode), self.assertRaises(ValueError): audit.audit_graph(graph, self.versions)

    def plugins(self):
        names = list(audit.ANDROID_PLUGINS)
        return {'plugins': {'android': [{'name': name, 'native_build': audit.ANDROID_PLUGINS[name],
                 'dependencies': sorted(audit.JNI_PLUGIN_DEPENDENCIES.get(name, set()))} for name in names]},
                'dependencyGraph': [{'name': name, 'dependencies': sorted(audit.JNI_PLUGIN_DEPENDENCIES.get(name, set()))}
                                    for name in names]}

    def test_official_plugins_retained(self):
        self.assertEqual(len(audit.audit_plugins(self.plugins())), 5)

    def test_missing_extra_or_misregistered_plugin_rejected(self):
        for mode in ('extra', 'removed', 'not_native', 'duplicate', 'jni_graph', 'registration_graph'):
            plugins = self.plugins()
            if mode == 'extra': plugins['dependencyGraph'].append({'name': 'jni_util', 'dependencies': []})
            if mode == 'removed': plugins['plugins']['android'].pop(1)
            if mode == 'not_native': plugins['plugins']['android'][0]['native_build'] = False
            if mode == 'duplicate': plugins['plugins']['android'].append(plugins['plugins']['android'][0])
            if mode == 'jni_graph':
                next(entry for entry in plugins['dependencyGraph'] if entry['name'] == 'jni_flutter')['dependencies'] = []
            if mode == 'registration_graph':
                next(entry for entry in plugins['plugins']['android'] if entry['name'] == 'path_provider_android')['dependencies'] = []
            with self.subTest(mode=mode), self.assertRaises(ValueError): audit.audit_plugins(plugins)

    def test_maven_resolved_versions(self):
        text = 'releaseRuntimeClasspath - Resolved\n+--- a.b:c:1.0 -> 2.0 (*)\n\\--- d:e:3.0\nBUILD SUCCESSFUL'
        self.assertEqual(audit.maven_coordinates(text), {'a.b:c:2.0', 'd:e:3.0'})
        audit.audit_maven(text, {'resolved_maven_coordinates': ['a.b:c:2.0', 'd:e:3.0']})
        with self.assertRaises(ValueError): audit.audit_maven(text, self.baseline)
        with self.assertRaises(ValueError): audit.maven_coordinates(text.replace('BUILD SUCCESSFUL', 'BUILD FAILED'))

    def jni_elf(self, abi, *, alignment=16384, machine=None):
        data = bytearray(120)
        data[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', data, 18, machine or {'arm64-v8a': 183, 'x86_64': 62}[abi])
        struct.pack_into('<Q', data, 32, 64)
        struct.pack_into('<HH', data, 54, 56, 1)
        struct.pack_into('<IIQQQQQQ', data, 64, 1, 5, 0, 0, 0, 120, 120, alignment)
        return bytes(data)

    def dex(self, descriptors, *, definitions=True):
        descriptors = sorted(descriptors)
        count = len(descriptors)
        string_off, type_off, class_off = 112, 112 + count * 4, 112 + count * 8
        data = bytearray(class_off + count * 32)
        data[:8] = b'dex\n039\x00'
        struct.pack_into('<II', data, 36, 112, 0x12345678)
        struct.pack_into('<IIII', data, 56, count, string_off, count, type_off)
        struct.pack_into('<II', data, 96, count if definitions else 0, class_off)
        for index, value in enumerate(descriptors):
            struct.pack_into('<I', data, string_off + index * 4, len(data))
            struct.pack_into('<I', data, type_off + index * 4, index)
            struct.pack_into('<I', data, class_off + index * 32, index)
            self.assertLess(len(value), 128)
            data.extend(bytes([len(value)]) + value + b'\x00')
        struct.pack_into('<I', data, 32, len(data))
        return bytes(data)

    def test_dex_references_do_not_count_as_class_definitions(self):
        self.assertEqual(audit.dex_defined_classes(self.dex(audit.JNI_CLASSES)), audit.JNI_CLASSES)
        self.assertEqual(audit.dex_defined_classes(self.dex(audit.JNI_CLASSES, definitions=False)), set())
        good = self.dex(audit.JNI_CLASSES)
        for bad in (good[:80], b'not DEX' + good[7:], good[:-1], good[:96] + b'\xff' * 8 + good[104:]):
            with self.assertRaises(ValueError): audit.dex_defined_classes(bad)

    def test_apk_requires_exact_jni_and_application_closure(self):
        for bad in (None, 'jni_missing', 'app_missing', 'class_missing', 'reference_only', 'extra_abi', 'extra_path',
                    'wrong_machine', 'not_elf', 'page_size', 'missing_load', 'duplicate', 'uncompressed'):
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'test.apk'
                with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as apk:
                    if bad != 'app_missing': apk.writestr('lib/x86_64/libiotools.so', b'app JNI is mandatory')
                    native = self.jni_elf('x86_64', alignment=4096 if bad == 'page_size' else 16384,
                                          machine=183 if bad == 'wrong_machine' else None)
                    if bad == 'not_elf': native = b'not ELF'
                    if bad == 'missing_load': native = native[:64] + bytes(56)
                    if bad != 'jni_missing':
                        apk.writestr('lib/x86_64/libdartjni.so', native,
                                     compress_type=zipfile.ZIP_STORED if bad == 'uncompressed' else zipfile.ZIP_DEFLATED)
                    if bad == 'extra_abi': apk.writestr('lib/arm64-v8a/libdartjni.so', self.jni_elf('arm64-v8a'))
                    if bad == 'extra_path': apk.writestr('assets/libdartjni.so', native)
                    if bad == 'duplicate':
                        import warnings
                        with warnings.catch_warnings():
                            warnings.simplefilter('ignore', UserWarning)
                            apk.writestr('lib/x86_64/libdartjni.so', native)
                    apk.writestr('classes.dex', self.dex(audit.JNI_CLASSES if bad != 'class_missing' else [],
                                                        definitions=bad != 'reference_only'))
                if bad is not None:
                    with self.assertRaises(ValueError): audit.audit_apk(path)
                else: self.assertEqual(len(audit.audit_apk(path)['sha256']), 64)

    def test_apk_accepts_two_abi_closure_with_classes_split_across_dex(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'test.apk'
            with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as apk:
                for abi in ('arm64-v8a', 'x86_64'):
                    apk.writestr(f'lib/{abi}/libiotools.so', b'app')
                    apk.writestr(f'lib/{abi}/libdartjni.so', self.jni_elf(abi))
                for index, descriptor in enumerate(sorted(audit.JNI_CLASSES), 1):
                    apk.writestr(f'classes{index}.dex', self.dex([descriptor]))
            self.assertEqual(audit.audit_apk(path)['jni_abis'], ['arm64-v8a', 'x86_64'])

    def test_jni_uses_the_app_pinned_ndk(self):
        root = (audit.ROOT / 'mobile/android/build.gradle').read_text()
        app = (audit.ROOT / 'mobile/android/app/build.gradle').read_text()
        self.assertIn("subproject.name == 'jni'", root)
        self.assertIn("plugins.withId('com.android.library')", root)
        self.assertIn("getByName('androidComponents').finalizeDsl", root)
        pin = re.search(r"android.ndkVersion = '([^']+)'", root)[1]
        self.assertEqual(pin, '28.1.13356709')
        self.assertIn("ndkVersion '" + pin + "'", app)

if __name__ == '__main__': unittest.main()
