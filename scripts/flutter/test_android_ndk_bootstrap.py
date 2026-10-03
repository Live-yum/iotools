#!/usr/bin/env python3
"""Hermetic NDK/bootstrap-order tests; fake sdkmanager, no Android SDK or network."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest

import android_ndk_bootstrap as bootstrap

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = Path(bootstrap.__file__)


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='iotools-ndk-')
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.sdk = self.root / 'sdk with spaces'
        self.ndk = self.sdk / 'ndk' / bootstrap.NDK_VERSION
        self.evidence = self.root / 'evidence'
        self.env_file = self.root / 'github-env'
        self.env_file.write_text('EXISTING=value\n')
        self.trace = self.root / 'sdkmanager-trace.json'
        self.license = self.sdk / 'licenses/android-sdk-license'
        self.license.parent.mkdir(parents=True)
        self.license.write_text('already-accepted-license-fixture\n')
        self.sdkmanager = self.sdk / 'cmdline-tools/latest/bin/sdkmanager'
        self.sdkmanager.parent.mkdir(parents=True)
        self.sdkmanager.write_text('#!' + sys.executable + '\n' + textwrap.dedent('''\
            import json, os, pathlib, sys, time
            with pathlib.Path(os.environ['TRACE']).open('a') as trace:
                trace.write(json.dumps({'argv': sys.argv[1:], 'stdin': sys.stdin.read()}) + '\\n')
            print('sdkmanager fixture output', flush=True)
            mode = os.environ['MODE']
            if mode == 'timeout':
                time.sleep(30)
            if mode == 'fail':
                print('Archive is not a ZIP archive', flush=True)
                sys.exit(42)
            if mode != 'no-install':
                ndk = pathlib.Path(os.environ['FAKE_NDK'])
                ndk.mkdir(parents=True)
                revision = '27.0.0' if mode == 'wrong-revision' else os.environ['REVISION']
                (ndk/'source.properties').write_text('Pkg.Revision = ' + revision + '\\n')
                if mode != 'partial':
                    toolchain = ndk/os.environ['TOOLCHAIN']
                    toolchain.mkdir(parents=True)
                    for name in json.loads(os.environ['TOOLS']):
                        tool = toolchain/name
                        tool.write_text('#!/bin/sh\\nexit 0\\n')
                        tool.chmod(0o700)
            if mode == 'install-then-fail':
                sys.exit(43)
            '''))
        self.sdkmanager.chmod(0o700)

    def installed(self, revision=bootstrap.NDK_VERSION):
        (self.ndk / bootstrap.TOOLCHAIN).mkdir(parents=True)
        (self.ndk / 'source.properties').write_text(f'Pkg.Revision = {revision}\n')
        for name in bootstrap.REQUIRED_TOOLS:
            tool = self.ndk / bootstrap.TOOLCHAIN / name
            tool.write_text('#!/bin/sh\nexit 0\n')
            tool.chmod(0o700)

    def run_case(self, mode='install', timeout=5):
        env = os.environ | {
            'TRACE': str(self.trace), 'MODE': mode, 'FAKE_NDK': str(self.ndk),
            'REVISION': bootstrap.NDK_VERSION, 'TOOLCHAIN': str(bootstrap.TOOLCHAIN),
            'TOOLS': json.dumps(bootstrap.REQUIRED_TOOLS),
        }
        result = subprocess.run([
            sys.executable, str(SCRIPT), '--sdk-root', str(self.sdk),
            '--github-env', str(self.env_file), '--evidence-dir', str(self.evidence),
            '--install-timeout', str(timeout),
        ], env=env, capture_output=True, text=True, timeout=10)
        report = json.loads((self.evidence / 'result.json').read_text())
        self.assertEqual(json.loads(result.stdout), report)
        if result.returncode:
            self.assertEqual(self.env_file.read_text(), 'EXISTING=value\n')
            self.assertEqual(report['status'], 'failed')
        else:
            self.assertEqual(self.env_file.read_text(), f'EXISTING=value\nANDROID_NDK_HOME={self.ndk}\n')
            self.assertEqual(report['status'], 'validated')
            self.assertEqual(report['final_validation']['revision'], bootstrap.NDK_VERSION)
            self.assertEqual(report['final_validation']['errors'], [])
        if report['installation'] is not None:
            trace = [json.loads(line) for line in self.trace.read_text().splitlines()]
            self.assertEqual(trace, [{'argv': [f'--sdk_root={self.sdk}', f'ndk;{bootstrap.NDK_VERSION}'], 'stdin': ''}])
            log = (self.evidence / 'sdkmanager.log').read_text()
            self.assertIn('sdkmanager fixture output', log)
            self.assertIn(f"sdkmanager_exit={report['installation']['exit_code']}\n", log)
        return result, report

    def test_valid_installed_ndk_never_invokes_sdkmanager(self):
        self.installed()
        self.license.unlink()
        self.sdkmanager.unlink()
        result, report = self.run_case()
        self.assertEqual(result.returncode, 0)
        self.assertIsNone(report['installation'])
        self.assertFalse(self.trace.exists())

    def test_absent_ndk_is_provisioned_once_then_reused(self):
        result, report = self.run_case()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report['installation']['exit_code'], 0)
        self.assertIn('Pkg.Revision = ' + bootstrap.NDK_VERSION, report['final_validation']['source_properties'])
        self.env_file.write_text('EXISTING=value\n')
        self.trace.unlink()
        result, report = self.run_case(mode='fail')
        self.assertEqual(result.returncode, 0)
        self.assertIsNone(report['installation'])
        self.assertFalse(self.trace.exists())

    def test_existing_partial_install_is_preserved_and_rejected(self):
        self.ndk.mkdir(parents=True)
        sentinel = self.ndk / 'partial-download'
        sentinel.write_text('preserve')
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(report['installation'])
        self.assertFalse(self.trace.exists())
        self.assertEqual(sentinel.read_text(), 'preserve')

    def test_existing_wrong_revision_is_preserved_and_rejected(self):
        self.installed('27.0.0')
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report['initial_validation']['revision'], '27.0.0')
        self.assertIsNone(report['installation'])
        self.assertFalse(self.trace.exists())
        self.assertEqual((self.ndk / 'source.properties').read_text(), 'Pkg.Revision = 27.0.0\n')

    def test_existing_nonexecutable_tool_is_rejected(self):
        self.installed()
        tool = self.ndk / bootstrap.TOOLCHAIN / 'llvm-readelf'
        tool.chmod(0o600)
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f'Missing executable: {tool}', report['initial_validation']['errors'])
        self.assertIsNone(report['installation'])

    def test_other_ndk_revision_is_never_selected(self):
        other = self.sdk / 'ndk/27.0.0'
        other.mkdir(parents=True)
        (other / 'source.properties').write_text('Pkg.Revision = 27.0.0\n')
        result, report = self.run_case('fail')
        self.assertEqual(result.returncode, 42)
        self.assertEqual(report['expected_revision'], bootstrap.NDK_VERSION)
        self.assertEqual((other / 'source.properties').read_text(), 'Pkg.Revision = 27.0.0\n')

    def test_corrupt_properties_are_rejected(self):
        self.installed()
        (self.ndk / 'source.properties').write_bytes(b'\xff\x00')
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(report['installation'])

    def test_duplicate_revision_is_rejected(self):
        self.installed()
        (self.ndk / 'source.properties').write_text(f'Pkg.Revision = {bootstrap.NDK_VERSION}\n' * 2)
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(report['installation'])

    def test_no_preaccepted_license_does_not_launch_sdkmanager(self):
        self.license.unlink()
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('already accepted', report['error'])
        self.assertFalse(self.trace.exists())

    def test_no_installed_sdkmanager_is_rejected(self):
        self.sdkmanager.unlink()
        result, report = self.run_case()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('command-line tools missing', report['error'])
        self.assertFalse(self.trace.exists())

    def test_successful_command_with_incomplete_ndk_is_rejected(self):
        result, report = self.run_case('partial')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report['installation']['exit_code'], 0)
        self.assertEqual(report['final_validation']['revision'], bootstrap.NDK_VERSION)
        self.assertTrue(report['final_validation']['errors'])

    def test_successful_command_with_no_install_is_rejected(self):
        result, report = self.run_case('no-install')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report['installation']['exit_code'], 0)
        self.assertIsNone(report['final_validation']['revision'])

    def test_successful_command_with_wrong_revision_is_rejected(self):
        result, report = self.run_case('wrong-revision')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report['installation']['exit_code'], 0)
        self.assertEqual(report['final_validation']['revision'], '27.0.0')

    def test_sdkmanager_error_retains_exit_and_log(self):
        result, report = self.run_case('fail')
        self.assertEqual(result.returncode, 42)
        self.assertEqual(report['installation']['exit_code'], 42)
        self.assertIn('Archive is not a ZIP archive', (self.evidence / 'sdkmanager.log').read_text())

    def test_valid_tree_cannot_hide_sdkmanager_failure(self):
        result, report = self.run_case('install-then-fail')
        self.assertEqual(result.returncode, 43)
        self.assertEqual(report['final_validation']['revision'], bootstrap.NDK_VERSION)
        self.assertEqual(report['final_validation']['errors'], [])

    def test_timeout_fails_and_records_exit(self):
        result, report = self.run_case('timeout', timeout=0.2)
        self.assertEqual(result.returncode, 124)
        self.assertTrue(report['installation']['timed_out'])
        self.assertNotEqual(report['installation']['process_returncode'], 0)


class WorkflowTests(unittest.TestCase):
    def test_bootstrap_precedes_any_project_gradle_or_native_build(self):
        workflow = (ROOT / '.github/workflows/android.yml').read_text()
        marker = '      - name: Validate or provision pinned Android NDK\n'
        self.assertEqual(workflow.count(marker), 1)
        before, after = workflow.split(marker)
        bootstrap_step, remaining = after.split('      - name:', 1)
        self.assertIn('python3 scripts/flutter/test_android_ndk_bootstrap.py', bootstrap_step)
        self.assertIn('python3 scripts/flutter/android_ndk_bootstrap.py --sdk-root "$ANDROID_HOME" --github-env "$GITHUB_ENV"', bootstrap_step)
        self.assertNotRegex(before, r'(?m)^\s+(?:gradle |\./gradlew |flutter build |bash scripts/android/build-native)')
        for command in ('gradle -p android wrapper', 'flutter build apk', './gradlew ', 'bash scripts/android/build-native.sh'):
            self.assertIn(command, remaining)
        self.assertIn('if: always()\n        with:\n          name: iotools-flutter-evidence-', workflow)
        self.assertIn('            android-evidence/\n', workflow)
        self.assertNotIn('Select installed Android NDK', workflow)

    def test_native_go_and_gradle_keep_identical_pin(self):
        gradle = (ROOT / 'mobile/android/app/build.gradle').read_text()
        native = (ROOT / 'scripts/android/build-native.sh').read_text()
        self.assertEqual(re.findall(r"ndkVersion '([^']+)'", gradle), [bootstrap.NDK_VERSION])
        self.assertIn('/ndk/' + bootstrap.NDK_VERSION, native)
        self.assertIn('ANDROID_NDK_HOME', native)
        for tool in ('aarch64-linux-android26-clang', 'x86_64-linux-android26-clang', 'llvm-readelf'):
            self.assertIn(tool, bootstrap.REQUIRED_TOOLS)


if __name__ == '__main__':
    unittest.main()
