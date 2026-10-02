#!/usr/bin/env python3
import json
import re
import hashlib
from pathlib import Path
import tempfile
import unittest
import zipfile
import android_dependency_audit as audit

class DependencyAuditTests(unittest.TestCase):
    def setUp(self):
        self.baseline = json.loads(audit.BASELINE.read_text())
        self.baseline.pop('lock_entry_sha256')
        self.versions = {name: version for name, version in self.baseline['lock_versions'].items()
                         if name not in self.baseline['allowed_removed']}
        self.versions['path_provider_android'] = '2.2.23'

    def lock(self, versions=None):
        return 'packages:\n' + ''.join(f'  {name}:\n    dependency: transitive\n    version: "{version}"\n'
            for name, version in (versions or self.versions).items()) + 'sdks:\n  dart: ">=3.9.0"\n'

    def test_exact_isolation(self):
        self.assertEqual(audit.audit_lock(self.lock(), self.baseline)['versions'], self.versions)

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
                             ('path_provider_android', '2.3.1'), ('jni', '1.1.0'), ('new_package', '1.0.0')]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                audit.audit_lock(self.lock(dict(self.versions, **{name: value})), self.baseline)

    def test_other_removed_dependency_rejected(self):
        values = dict(self.versions)
        del values['http']
        with self.assertRaises(ValueError): audit.audit_lock(self.lock(values), self.baseline)

    def graph(self):
        return {'packages': [{'name': name, 'version': version, 'kind': 'transitive', 'dependencies':
                 ['flutter', 'path_provider_platform_interface'] if name == 'path_provider_android' else []}
                for name, version in self.versions.items()] + [{'name': 'iotools_mobile', 'version': '0.3.0', 'kind': 'root'}]}

    def test_graph_matches_lock(self):
        audit.audit_graph(self.graph(), self.versions)

    def test_graph_drift_and_duplicates_rejected(self):
        for mode in ('duplicate', 'version', 'dependency'):
            graph = self.graph()
            if mode == 'duplicate': graph['packages'].append(graph['packages'][0])
            if mode == 'version': graph['packages'][0]['version'] = '0.0.1'
            if mode == 'dependency':
                next(x for x in graph['packages'] if x['name'] == 'path_provider_android')['dependencies'].append('jni')
            with self.subTest(mode=mode), self.assertRaises(ValueError): audit.audit_graph(graph, self.versions)

    def plugins(self):
        names = ['file_selector_android', 'path_provider_android', 'integration_test']
        return {'plugins': {'android': [{'name': name, 'native_build': True} for name in names]},
                'dependencyGraph': [{'name': name} for name in names]}

    def test_official_plugins_retained(self):
        self.assertEqual(len(audit.audit_plugins(self.plugins())), 3)

    def test_jni_or_missing_official_plugin_rejected(self):
        for mode in ('jni', 'removed', 'not_native'):
            plugins = self.plugins()
            if mode == 'jni': plugins['dependencyGraph'].append({'name': 'jni_flutter'})
            if mode == 'removed': plugins['plugins']['android'].pop(1)
            if mode == 'not_native': plugins['plugins']['android'][1]['native_build'] = False
            with self.subTest(mode=mode), self.assertRaises(ValueError): audit.audit_plugins(plugins)

    def test_maven_resolved_versions(self):
        text = 'releaseRuntimeClasspath - Resolved\n+--- a.b:c:1.0 -> 2.0 (*)\n\\--- d:e:3.0\nBUILD SUCCESSFUL'
        self.assertEqual(audit.maven_coordinates(text), {'a.b:c:2.0', 'd:e:3.0'})
        audit.audit_maven(text, {'resolved_maven_coordinates': ['a.b:c:2.0', 'd:e:3.0']})
        with self.assertRaises(ValueError): audit.audit_maven(text, self.baseline)
        with self.assertRaises(ValueError): audit.maven_coordinates(text.replace('BUILD SUCCESSFUL', 'BUILD FAILED'))

    def test_apk_rejects_external_jni_preserves_application_jni(self):
        for bad in (None, 'lib/x86_64/libdartjni.so', 'classes.dex'):
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'test.apk'
                with zipfile.ZipFile(path, 'w') as apk:
                    apk.writestr('lib/x86_64/libiotools.so', b'app JNI is mandatory')
                    if bad: apk.writestr(bad, b'Lcom/github/dart_lang/jni/JniPlugin;')
                if bad:
                    with self.assertRaises(ValueError): audit.audit_apk(path)
                else: self.assertEqual(len(audit.audit_apk(path)['sha256']), 64)

if __name__ == '__main__': unittest.main()
