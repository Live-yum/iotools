import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import diagnose_ios_host as diagnostics


class IOSHostDiagnosticsTests(unittest.TestCase):
    def test_inventory_is_read_only_and_pinned(self):
        commands = dict(diagnostics.COMMANDS)
        self.assertEqual(commands['devices'], ['xcrun', 'simctl', 'list', 'devices', 'available', '--json'])
        self.assertEqual(commands['runtimes'], ['xcrun', 'simctl', 'list', 'runtimes', '--json'])
        self.assertEqual(diagnostics.DEVELOPER_DIR, '/Applications/Xcode_26.2.app/Contents/Developer')
        for command in commands.values():
            self.assertFalse(set(command) & {'sudo', 'boot', 'create', 'delete', 'erase', 'shutdown', 'killall', '-switch'})

    def test_timeout_and_partial_output_remain_in_evidence_without_retry(self):
        calls = []
        def run(command, **kwargs):
            calls.append(command)
            self.assertEqual(kwargs['timeout'], 30)
            self.assertFalse(kwargs['check'])
            self.assertEqual(kwargs['env']['DEVELOPER_DIR'], diagnostics.DEVELOPER_DIR)
            kwargs['stdout'].write(b'partial inventory')
            if command == dict(diagnostics.COMMANDS)['devices']:
                raise subprocess.TimeoutExpired(command, 30)
            return subprocess.CompletedProcess(command, 0)
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(diagnostics, 'OUTPUT', Path(directory)), patch.object(diagnostics.subprocess, 'run', side_effect=run), contextlib.redirect_stdout(io.StringIO()):
                diagnostics.main()
            report = json.loads((Path(directory)/'report.json').read_text())
            self.assertEqual(calls, [command for _, command in diagnostics.COMMANDS])
            self.assertIn('no app or simulator acceptance', report['scope'])
            device = next(row for row in report['queries'] if row['name'] == 'devices')
            self.assertTrue(device['timed_out'])
            self.assertIsNone(device['exit_code'])
            self.assertEqual((Path(directory)/device['stdout']).read_bytes(), b'partial inventory')
            self.assertEqual(len(report['queries']), len(diagnostics.COMMANDS))

    def test_missing_tools_are_recorded_without_mutation_fallback(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(diagnostics, 'OUTPUT', Path(directory)), patch.object(diagnostics.subprocess, 'run', side_effect=FileNotFoundError('tool unavailable')) as run, contextlib.redirect_stdout(io.StringIO()):
                diagnostics.main()
            report = json.loads((Path(directory)/'report.json').read_text())
            self.assertEqual(run.call_count, len(diagnostics.COMMANDS))
            self.assertTrue(all(row['exit_code'] is None for row in report['queries']))
            self.assertTrue(all(not row['timed_out'] for row in report['queries']))


if __name__ == '__main__':
    unittest.main()
