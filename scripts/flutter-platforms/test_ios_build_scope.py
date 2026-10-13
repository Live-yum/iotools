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
            self.assertEqual(run.call_count, 4)
            self.assertTrue(all(call.kwargs['timeout'] == 15 for call in run.call_args_list))
            self.assertTrue(all(not {'install', 'boot', 'shutdown', 'erase'} & set(call.args) for call in run.call_args_list))
            self.assertEqual(len(list(Path(folder).glob('failure-*.txt'))), 4)

    def test_launch_summary_drops_arbitrary_payload_and_keeps_only_allowlisted_fields(self):
        raw = ('2026-10-13 13:32:01.123 Df Runner[123:abcd] launching email=private@example.com '
               'https://private.invalid/secret /Users/private/file medical-payload\n'
               '2026-10-13 13:32:02.123 E  SpringBoard[12:abc] io.github.liveyum.iotools denied '
               'Error Domain=FBSOpenApplicationServiceErrorDomain Code=4 "private secret body" business Code=123456\n'
               '2026-10-13 13:32:03.123 E SpringBoard[12:abc] other.bundle failed Code=99\n'
               'arbitrary secret startup output\n')
        output = verifier.launch_event_summary(raw)
        rows = [json.loads(line) for line in output.splitlines()]
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[0]['event_hints'], ['launch'])
        self.assertEqual(rows[1]['system_error_codes'], [4])
        for secret in ('private', 'medical', 'other.bundle', '99', 'arbitrary', 'email=', '123456'):
            self.assertNotIn(secret, output)
        self.assertTrue(all(row['body'] == '<omitted>' for row in rows))

    def test_launch_summary_keeps_complete_latest_lines_within_16k(self):
        raw = ''.join('2026-10-13 13:32:01.123 Df Runner[123:abc] launching secret\n' for _ in range(1000))
        raw += '2026-10-13 13:33:02.456 A  runningboardd[12:abc] io.github.liveyum.iotools exited\n'
        output = verifier.launch_event_summary(raw.encode())
        self.assertLessEqual(len(output.encode()), 16384)
        rows = [json.loads(line) for line in output.splitlines()]
        self.assertEqual(rows[-1]['timestamp'], '2026-10-13 13:33:02.456')
        self.assertEqual(rows[-1]['event_hints'], ['exit'])
        self.assertNotIn('secret', output)

    def test_launch_diagnostic_timeout_records_metadata_without_raw_artifact(self):
        raw = b'2026-10-13 13:32:01.123 Df Runner[123:abc] launching private-business-payload\n'
        with tempfile.TemporaryDirectory() as folder:
            evidence = Path(folder)
            def command(*args, **kwargs):
                if 'log' in args and 'Runner' in args[-1]:
                    raise subprocess.TimeoutExpired(args, 15, output=raw)
                return ''
            with patch.dict(os.environ, {'IOTOOLS_IOS_SIMULATOR_UDID': '11111111-2222-3333-4444-555555555555'}), \
                 patch.object(verifier, 'run', side_effect=command) as run:
                verifier.simulator_failure_diagnostics(evidence)
            self.assertEqual(run.call_count, 4)
            record = json.loads((evidence/'failure-launch-log-command.json').read_text())
            self.assertEqual(record['status'], 'timeout')
            self.assertEqual(record['timeout_seconds'], 15)
            self.assertGreaterEqual(record['finished_unix'], record['started_unix'])
            self.assertGreaterEqual(record['elapsed_seconds'], 0)
            self.assertEqual(record['command'][:3], ['xcrun', 'simctl', 'spawn'])
            self.assertIn('eventMessage CONTAINS[c] "io.github.liveyum.iotools"', record['command'][-1])
            for path in evidence.iterdir():
                self.assertNotIn('private-business-payload', path.read_text())

    def test_normal_launch_timeout_is_one_attempt_at_60_seconds(self):
        udid = '11111111-2222-3333-4444-555555555555'
        original = subprocess.TimeoutExpired(['simctl', 'launch'], 60)
        def command(*args, **kwargs):
            if args[1:4] == ('simctl', 'list', 'devices'):
                return json.dumps({'devices': {'com.apple.CoreSimulator.SimRuntime.iOS-26-2': [{
                    'name': 'iPhone SE (3rd generation)', 'udid': udid, 'isAvailable': True, 'state': 'Booted'}]}})
            if 'launch' in args:
                self.assertEqual(kwargs['timeout'], 60)
                raise original
            return ''
        with tempfile.TemporaryDirectory() as folder, \
             patch.object(verifier, 'remaining', return_value=900), \
             patch.object(verifier, 'run', side_effect=command) as run:
            report = {}
            with self.assertRaises(subprocess.TimeoutExpired) as caught:
                verifier.normal_launch_simulator(Path('Runner.app'), {'CFBundleIdentifier': 'io.github.liveyum.iotools'},
                    Path(folder), report, lambda: None, '26.2', udid, 1200)
            self.assertIs(caught.exception, original)
            self.assertEqual(sum('launch' in call.args for call in run.call_args_list), 1)
            self.assertNotIn('normal_launch', report)
            self.assertNotIn('host_tests', report)

    def test_failed_launch_remains_original_failure_despite_diagnostic_failures(self):
        original = subprocess.TimeoutExpired(['xcrun', 'simctl', 'launch'], 60)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            with patch.object(verifier, 'ROOT', root), \
                 patch.object(verifier.platform, 'system', return_value='Darwin'), \
                 patch.dict(os.environ, {'IOTOOLS_SHA': 'test-sha', 'IOTOOLS_IOS_SIMULATOR_UDID': '11111111-2222-3333-4444-555555555555'}), \
                 patch.object(sys, 'argv', ['verify-ios.py', 'simulator']), \
                 patch.object(verifier, 'verify_bundle', return_value=({'CFBundleIdentifier': 'io.github.liveyum.iotools'}, {})), \
                 patch.object(verifier, 'test_simulator', side_effect=original), \
                 patch.object(verifier, 'run', side_effect=subprocess.TimeoutExpired(['diagnostic'], 15)):
                with self.assertRaises(subprocess.TimeoutExpired) as caught:
                    verifier.main()
            self.assertIs(caught.exception, original)
            report = json.loads((root/'platform-evidence/ios/simulator.json').read_text())
            self.assertEqual(report['status'], 'failed')
            self.assertNotIn('host_tests', report)
            self.assertFalse(list((root/'platform-dist').glob('*')))

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
