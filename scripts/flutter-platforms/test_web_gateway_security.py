"""Security-probe failures stay failures; diagnostics must never convert resets to403."""
import importlib.util
import json
import os
from pathlib import Path
import queue
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('gateway_verifier', Path(__file__).with_name('verify-web-gateway.py'))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class FakeGateway:
    def __init__(self, failure=None, status=403, changed=False, crashed=False):
        self.failure, self.status, self.changed, self.crashed = failure, status, changed, crashed
        self.events, self.probes = [], []
        self.reads = 0
        self.failed = False

    def record(self, value):
        self.events.append(dict(value))

    def process_exit(self):
        return 23 if self.failed and self.crashed else None

    def post(self, path, value):
        assert path == '/api/platform' and value == {'method': 'settings.get'}
        self.reads += 1
        if self.failed and self.crashed:
            raise ConnectionRefusedError('fixture process exited')
        return {'data': {'theme': 'dark' if self.changed and self.probes else 'light'}}

    def request(self, method, path, body, headers):
        assert (method, path, body) == ('POST', '/api/platform', b'{"method":"settings.get"}')
        self.probes.append(headers)
        if self.failure is not None and 'Host' in headers:
            self.failed = True
            raise self.failure
        return self.status, {}, b'denied'


class SecurityProbeTests(unittest.TestCase):
    def setUp(self):
        verifier.Target.received = queue.Queue()

    def test_all_four_explicit_denials_and_valid_reads_required(self):
        client = FakeGateway()
        verifier.check_security_boundaries(client)
        self.assertEqual(len(client.probes), 4)
        self.assertEqual(client.reads, 5)
        self.assertEqual([e['probe'] for e in client.events if e['state'] == 'passed'],
                         ['origin', 'host', 'csrf', 'cross-site'])
        self.assertTrue(all(e['status'] == 403 for e in client.events if e['state'] == 'passed'))

    def test_connection_reset_remains_exact_original_failure(self):
        failure = ConnectionResetError(10054, 'fixture peer reset')
        client = FakeGateway(failure=failure)
        with self.assertRaises(ConnectionResetError) as caught:
            verifier.check_security_boundaries(client)
        self.assertIs(caught.exception, failure)
        self.assertEqual(len(client.probes), 2)  # No retry of the rejected Host request.
        event = client.events[-1]
        self.assertEqual(event['probe'], 'host')
        self.assertEqual(event['state'], 'failed')
        self.assertEqual(event['subsequent_valid_read'], 'passed')
        self.assertIsNone(event['process_exit'])
        self.assertTrue(event['settings_unchanged'])
        self.assertEqual(event['protocol_requests'], 0)

    def test_crash_and_failed_following_read_are_recorded(self):
        client = FakeGateway(failure=ConnectionResetError('reset'), crashed=True)
        with self.assertRaises(ConnectionResetError):
            verifier.check_security_boundaries(client)
        self.assertEqual(client.events[-1]['process_exit'], 23)
        self.assertEqual(client.events[-1]['subsequent_valid_read'], 'failed')

    def test_success_http_status_cannot_pass_denial_gate(self):
        client = FakeGateway(status=200)
        with self.assertRaisesRegex(AssertionError, 'expected403'):
            verifier.check_security_boundaries(client)
        self.assertEqual(client.events[-1]['status'], 200)
        self.assertEqual(client.events[-1]['state'], 'failed')

    def test_403_with_changed_settings_is_rejected(self):
        client = FakeGateway(changed=True)
        with self.assertRaisesRegex(AssertionError, 'changed settings'):
            verifier.check_security_boundaries(client)
        self.assertFalse(client.events[-1]['settings_unchanged'])

    def test_403_with_protocol_side_effect_is_rejected(self):
        verifier.Target.received.put(('POST', '/write', b'fixture'))
        client = FakeGateway()
        with self.assertRaisesRegex(AssertionError, 'protocol I/O'):
            verifier.check_security_boundaries(client)
        self.assertEqual(client.events[-1]['protocol_requests'], 1)

    def test_original_failure_and_gateway_log_survive_temporary_data_cleanup(self):
        failure = ConnectionResetError(10054, 'injected fixture reset')
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            executable = root / 'gateway-fixture'
            executable.write_bytes(b'not executed: process is mocked')
            process = Mock(returncode=0)
            process.poll.return_value = None
            process.terminate.side_effect = lambda: setattr(process.poll, 'return_value', 0)
            def spawn(*args, **kwargs):
                kwargs['stdout'].write(b'iotools Web: http://127.0.0.1:12345\n')
                kwargs['stdout'].flush()
                return process
            target = Mock()
            previous = Path.cwd()
            try:
                os.chdir(root)
                with patch.object(sys, 'argv', ['verify-web-gateway.py', str(executable)]), \
                     patch.object(verifier.http.server, 'ThreadingHTTPServer', return_value=target), \
                     patch.object(verifier.threading, 'Thread'), \
                     patch.object(verifier.subprocess, 'Popen', side_effect=spawn), \
                     patch.object(verifier, 'Gateway'), \
                     patch.object(verifier, 'run_checks', side_effect=failure):
                    with self.assertRaises(ConnectionResetError) as caught:
                        verifier.main()
                self.assertIs(caught.exception, failure)
                evidence = root / 'platform-evidence/web-gateway'
                report = json.loads((evidence / 'failure.json').read_text())
                self.assertEqual(report['error_type'], 'ConnectionResetError')
                self.assertIsNone(report['process_exit_before_cleanup'])
                self.assertIn('iotools Web', (evidence / 'gateway.log').read_text())
                process.terminate.assert_called_once()
                target.shutdown.assert_called_once()
                target.server_close.assert_called_once()
            finally:
                os.chdir(previous)


if __name__ == '__main__':
    unittest.main()
