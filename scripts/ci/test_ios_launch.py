#!/usr/bin/env python3
"""Hermetic tests of the actual simulator launch orchestration, not iOS acceptance.

Load just the production function definitions so these tests need neither Xcode
nor the release packager's imports. Only subprocess/time boundaries are faked.
"""
import ast
import json
from pathlib import Path
import re
import subprocess
import tempfile
import types
import unittest
from unittest.mock import Mock

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / 'scripts/flutter-platforms/verify-ios.py'
UDID = '48B2DA2E-AF18-4E36-BB25-B25969A4EBCF'
BUNDLE = 'io.github.liveyum.iotools'


def load_launch():
    wanted = {'normal_launch_simulator', 'select_simulator', 'require', 'diagnostic_text'}
    tree = ast.parse(SOURCE.read_text(encoding='utf-8'), filename=str(SOURCE))
    definitions = [node for node in tree.body
                   if isinstance(node, ast.FunctionDef) and node.name in wanted]
    if {node.name for node in definitions} != wanted:
        raise AssertionError('Simulator launch entry points changed; review these tests')
    scope = {'json': json, 're': re, 'subprocess': subprocess,
             'time': types.SimpleNamespace(sleep=Mock()),
             'remaining': Mock(return_value=1000)}
    exec(compile(ast.Module(body=definitions, type_ignores=[]), str(SOURCE), 'exec'), scope)
    return scope


class SimulatorLaunchTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.evidence = Path(self.temp.name)
        self.scope = load_launch()
        self.failures = {}
        self.services = f'123\t0\tUIKitApplication:{BUNDLE}\n'
        self.operation = Mock(side_effect=self.fake_operation)
        self.run = Mock(side_effect=self.fake_run)
        self.scope.update(simulator_operation=self.operation, run=self.run)
        self.report = {}
        self.save = Mock()

    def fake_operation(self, evidence, label, *args, timeout):
        if label in self.failures:
            raise self.failures[label]
        if label == 'normal-launch-devices':
            return json.dumps({'devices': {'com.apple.CoreSimulator.SimRuntime.iOS-26-2': [
                {'udid': UDID, 'name': 'iPhone SE (3rd generation)',
                 'isAvailable': True, 'state': 'Shutdown'}]}})
        return f'{BUNDLE}: 123' if label == 'simulator-launch' else ''

    def fake_run(self, *args, timeout):
        if 'screenshot' in args and 'screenshot' in self.failures:
            raise self.failures['screenshot']
        return self.services if 'launchctl' in args else ''

    def launch(self, deadline=None):
        return self.scope['normal_launch_simulator'](
            Path('Runner.app'), {'CFBundleIdentifier': BUNDLE}, self.evidence,
            self.report, self.save, '26.2', UDID, deadline)

    def calls(self, label):
        return [call for call in self.operation.call_args_list if call.args[1] == label]

    def test_cold_install_has_bounded_budget_and_no_retry(self):
        self.launch()
        self.assertEqual(len(self.calls('simulator-install')), 1)
        self.assertEqual(self.calls('simulator-install')[0].kwargs['timeout'], 300)
        self.assertEqual(len(self.calls('simulator-launch')), 1)
        self.assertEqual(len(self.calls('simulator-cleanup')), 1)
        self.assertEqual(self.report['normal_launch']['still_running_after_seconds'], 5)

    def test_install_budget_never_extends_host_deadline(self):
        self.scope['remaining'].return_value = 75
        self.launch(deadline=12345)
        self.assertEqual(self.calls('simulator-install')[0].kwargs['timeout'], 75)
        self.scope['remaining'].assert_called_with(12345)

    def test_install_timeout_is_not_retried_or_treated_as_launch(self):
        failure = subprocess.TimeoutExpired(['simctl', 'install'], 300)
        self.failures['simulator-install'] = failure
        with self.assertRaises(subprocess.TimeoutExpired) as caught:
            self.launch()
        self.assertIs(caught.exception, failure)
        self.assertEqual(len(self.calls('simulator-install')), 1)
        self.assertFalse(self.calls('simulator-launch'))
        self.assertNotIn('normal_launch', self.report)

    def test_cleanup_failure_preserves_app_exit_failure(self):
        self.services = ''
        self.failures['simulator-cleanup'] = subprocess.CalledProcessError(1, ['simctl', 'terminate'])
        with self.assertRaisesRegex(RuntimeError, 'App exited after launch'):
            self.launch()
        self.assertIn('cleanup_error', self.report['normal_launch'])
        self.assertNotIn('still_running_after_seconds', self.report['normal_launch'])

    def test_cleanup_failure_preserves_original_screenshot_exception(self):
        failure = subprocess.TimeoutExpired(['simctl', 'screenshot'], 120)
        self.failures['screenshot'] = failure
        self.failures['simulator-cleanup'] = subprocess.CalledProcessError(1, ['simctl', 'terminate'])
        with self.assertRaises(subprocess.TimeoutExpired) as caught:
            self.launch()
        self.assertIs(caught.exception, failure)
        self.assertEqual(len(self.calls('simulator-cleanup')), 1)

    def test_cleanup_failure_alone_still_fails_gate(self):
        failure = subprocess.CalledProcessError(1, ['simctl', 'terminate'])
        self.failures['simulator-cleanup'] = failure
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            self.launch()
        self.assertIs(caught.exception, failure)
        self.assertIn('cleanup_error', self.report['normal_launch'])

    def test_repeated_normal_launch_is_rejected(self):
        self.launch()
        calls = self.operation.call_count
        with self.assertRaisesRegex(RuntimeError, 'Refusing repeated normal launch'):
            self.launch()
        self.assertEqual(self.operation.call_count, calls)


if __name__ == '__main__':
    unittest.main()
