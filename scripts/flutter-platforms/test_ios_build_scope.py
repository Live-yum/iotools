"""Compile-only artifacts must never be represented as XCTest passes."""
import importlib.util
import json
import os
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('ios_scope_verifier', Path(__file__).with_name('verify-ios.py'))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class ArtifactScopeTests(unittest.TestCase):
    def test_install_timeout_output_preserved_without_retry(self):
        with tempfile.TemporaryDirectory() as folder:
            evidence = Path(folder)
            failure = subprocess.TimeoutExpired(['simctl', 'install'], 120, output=b'installd waiting')
            with patch.object(verifier, 'run', side_effect=failure) as run:
                with self.assertRaises(subprocess.TimeoutExpired):
                    verifier.simulator_operation(evidence, 'install', 'simctl', 'install')
            self.assertEqual(run.call_count, 1)
            self.assertEqual(run.call_args.kwargs['timeout'], 120)
            self.assertIn('installd waiting', (evidence/'install.txt').read_text())

    def test_failure_diagnostics_are_bounded_read_only_and_cannot_raise(self):
        with tempfile.TemporaryDirectory() as folder:
            with patch.dict(os.environ, {'IOTOOLS_IOS_SIMULATOR_UDID': '11111111-2222-3333-4444-555555555555'}), \
                 patch.object(verifier, 'run', side_effect=PermissionError('unavailable')) as run:
                verifier.simulator_failure_diagnostics(Path(folder))
            self.assertEqual(run.call_count, 3)
            self.assertTrue(all(call.kwargs['timeout'] == 15 for call in run.call_args_list))
            self.assertTrue(all(not {'install', 'boot', 'shutdown', 'erase'} & set(call.args) for call in run.call_args_list))
            self.assertEqual(len(list(Path(folder).glob('failure-*.txt'))), 3)

    def test_static_simulator_artifact_never_attempts_or_claims_runtime(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def command(*args, **kwargs):
                self.assertEqual(args[0], 'ditto')
                Path(args[-1]).write_bytes(b'static artifact')
                return ''
            with patch.object(verifier, 'ROOT', root), \
                 patch.object(verifier.platform, 'system', return_value='Darwin'), \
                 patch.dict(os.environ, {'IOTOOLS_SHA': 'static-test'}), \
                 patch.object(sys, 'argv', ['verify-ios.py', 'simulator-compiled']), \
                 patch.object(verifier, 'verify_bundle', return_value=({'CFBundleIdentifier': 'test.app'}, {'architectures': ['arm64']})) as bundle, \
                 patch.object(verifier, 'test_simulator') as simulation, \
                 patch.object(verifier, 'collect_notices', return_value={'go_license_files': 1}), \
                 patch.object(verifier, 'run', side_effect=command):
                verifier.main()
            self.assertEqual(bundle.call_args.args[1], 'simulator')
            simulation.assert_not_called()
            report = json.loads((root/'platform-dist/ios-simulator-compiled-static-test-manifest.json').read_text())
            self.assertEqual(report['status'], 'compiled_only_no_runtime_acceptance')
            self.assertNotIn('host_tests', report)
            self.assertNotIn('normal_launch', report)
            self.assertIn('integration_test', report['bundle_type'])
            self.assertIn('All five required host XCTest cases', report['remaining_gates'])

    def test_simulator_build_uses_simulator_path_and_does_not_claim_xctest(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def command(*args, **kwargs):
                self.assertEqual(args[0], 'ditto')
                Path(args[-1]).write_bytes(b'test artifact')
                return ''
            with patch.object(verifier, 'ROOT', root), \
                 patch.object(verifier.platform, 'system', return_value='Darwin'), \
                 patch.dict(os.environ, {'IOTOOLS_SHA': 'scope-test'}), \
                 patch.object(sys, 'argv', ['verify-ios.py', 'simulator-build']), \
                 patch.object(verifier, 'verify_bundle', return_value=({'CFBundleIdentifier': 'test.app'}, {'architectures': ['arm64']})) as bundle, \
                 patch.object(verifier, 'test_simulator') as simulation, \
                 patch.object(verifier, 'collect_notices', return_value={'go_license_files': 1}), \
                 patch.object(verifier, 'run', side_effect=command):
                verifier.main()
            self.assertEqual(bundle.call_args.args[:2], (root/'mobile/build/ios/iphonesimulator/Runner.app', 'simulator'))
            self.assertIs(simulation.call_args.kwargs['run_host_tests'], False)
            report = json.loads((root/'platform-dist/ios-simulator-build-scope-test-manifest.json').read_text())
            self.assertEqual(report['status'], 'compiled_and_normally_launched_only')
            self.assertNotIn('host_tests', report)
            self.assertIn('XCTest', (root/'platform-dist/ios-simulator-build-README.zh-CN.txt').read_text())

    def test_full_simulator_gate_remains_required_by_default(self):
        import inspect
        self.assertIs(inspect.signature(verifier.test_simulator).parameters['run_host_tests'].default, True)
        command = verifier.simulator_test_command('device-id', Path('results.xcresult'), 'arm64')
        self.assertIn('test', command)
        self.assertIn('-only-testing:RunnerTests', command)


if __name__ == '__main__':
    unittest.main()
