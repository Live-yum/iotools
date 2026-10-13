#!/usr/bin/env python3
"""Hermetic privacy, bounded-read and identity contracts; no device required."""
from pathlib import Path
import subprocess
import tempfile
import sys
import time
import unittest

from native_crash_evidence import (COMMAND, MAX_OUTPUT_BYTES, READ_TIMEOUT, TARGET,
                                   OutputLimitExceeded, collect, filtered_latest, read_bounded,
                                   suite_crash_pid)


def entry(process=TARGET, *, pid=123, timestamp='2026-10-13 12:00:00', extra=''):
    return f'''========================================
{timestamp} data_app_native_crash (text, 3210 bytes)
Process: {process}
PID: {pid}
Package: {process} v1 (1.0)
Build: private-device-details
*** *** *** *** *** *** *** *** *** *** *** *** *** *** *** ***
Cmdline: {process}
pid: {pid}, tid: {pid + 1}, name: 1.ui  >>> {process} <<<
signal 6 (SIGABRT), code -6 (SI_TKILL), fault addr --------
Abort message: 'Scudo ERROR: invalid chunk state when deallocating address secret-abort-payload'
    rax 7365637265746d65  rbx 6d6f72797061796c
backtrace:
    #00 pc 000000000007aabc  /apex/com.android.runtime/lib64/bionic/libc.so (abort+160) (BuildId: abcdef0123456789)
    #01 pc 0000000000123456  /data/app/private-install-token/{process}/lib/x86_64/libflutter.so (private-symbol+99) (BuildId: 0123456789abcdef)
stack:
    #00  00007fffeeeeeeee 7365637265746d65 /private/memory
memory near rax:
    0000000000008d34 7365637265746d65  secret-memory-payload
memory map (1 entry):
    00000000-ffffffff r-x 00000000 private-map-path
--------- tail end of log main
10-13 12:00:00.001 123 124 I app: {{"password":"secret-json-payload"}}
TOKEN=secret-environment-payload
{extra}
'''


class NativeCrashEvidenceTests(unittest.TestCase):
    def run_case(self, *, stdout=b'', returncode=0, error=None):
        calls = []
        def run(command, **kwargs):
            calls.append((command, kwargs))
            if error:
                raise error
            return subprocess.CompletedProcess(command, returncode, stdout, b'secret-adb-stderr')
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            suite_log = evidence / 'suite.log'
            suite_log.write_text(f'F/DEBUG   ( 999): pid: 123, tid: 124, name: 1.ui  >>> {TARGET} <<<\n')
            collect(evidence, suite_log, run=run)
            files = {path.name: path.read_text() for path in evidence.iterdir() if path != suite_log}
        self.assertEqual(calls, [(COMMAND, {'limit': MAX_OUTPUT_BYTES, 'timeout': READ_TIMEOUT})])
        self.assertLessEqual(READ_TIMEOUT, 4)
        self.assertNotIn('secret-adb-stderr', ''.join(files.values()))
        return files

    def test_target_stack_keeps_only_metadata(self):
        files = self.run_case(stdout=entry().encode())
        self.assertIn('status=captured\n', files['status.txt'])
        self.assertIn('thread_coverage=not_guaranteed\n', files['status.txt'])
        self.assertIn('correlation=suite_tombstone_pid\n', files['status.txt'])
        stack = files['stack.txt']
        self.assertIn(f'process={TARGET}\n', stack)
        self.assertIn('pid=123 tid=124 thread=1.ui\n', stack)
        self.assertIn('signal=6 name=SIGABRT code=-6\n', stack)
        self.assertIn('cause=invalid chunk state\n', stack)
        self.assertIn('frame=00 pc=000000000007aabc module=libc.so build_id=abcdef0123456789\n', stack)
        self.assertIn('frame=01 pc=0000000000123456 module=libflutter.so build_id=0123456789abcdef\n', stack)
        for unsafe in ('secret', 'private', 'password', 'TOKEN', 'rax', 'rbx',
                       '7365637265746d65', 'fault addr', 'memory map', 'abort+160'):
            self.assertNotIn(unsafe, stack)

    def test_only_exact_target_process_is_accepted(self):
        for process in ('other.app', TARGET + ':worker', TARGET + '.imitation'):
            with self.subTest(process=process):
                self.assertIsNone(filtered_latest(entry(process), '123'))
        raw = entry('foreign.app', pid=888) + entry() + entry('later.foreign.app', pid=999)
        self.assertEqual(filtered_latest(raw, '123'), filtered_latest(entry(), '123'))

    def test_inconsistent_or_missing_identity_rejects_entry(self):
        raw = entry()
        for bad in (raw.replace('Process: ' + TARGET, 'Process: foreign.app'),
                    raw.replace('Cmdline: ' + TARGET, 'Cmdline: foreign.app'),
                    raw.replace('PID: 123', 'PID: 999'),
                    raw.replace('>>> ' + TARGET + ' <<<', '>>> foreign.app <<<'),
                    raw.replace('Process: ' + TARGET + '\n', ''),
                    raw.replace('pid: 123, tid:', 'pid: unknown, tid:')):
            with self.subTest(raw=bad[:120]):
                self.assertIsNone(filtered_latest(bad, '123'))

    def test_android_10_entry_without_cmdline_is_supported(self):
        self.assertIsNotNone(filtered_latest(entry().replace('Cmdline: ' + TARGET + '\n', ''), '123'))

    def test_compressed_text_dropbox_entry_is_supported(self):
        raw = entry().replace('(text, 3210 bytes)', '(compressed text, 3210 bytes)')
        self.assertEqual(filtered_latest(raw, '123'), filtered_latest(entry(), '123'))

    def test_unwinder_notes_do_not_hide_frames_or_persist_access_advice(self):
        raw = entry().replace('backtrace:\n',
                              'backtrace:\n    NOTE: Function names and BuildId information is missing\n'
                              '    NOTE: On this device, run setenforce 0 to make the libraries readable.\n')
        self.assertEqual(filtered_latest(raw, '123'), filtered_latest(entry(), '123'))

    def test_all_available_same_process_threads_are_preserved(self):
        extra = f'''--- --- --- --- --- --- --- --- --- --- --- --- --- --- --- ---
pid: 123, tid: 125, name: 1.raster  >>> {TARGET} <<<
backtrace:
    #00 pc 0000000000111111 /system/lib64/libflutter.so (BuildId: aaaaaaaa)
--- --- --- --- --- --- --- --- --- --- --- --- --- --- --- ---
pid: 123, tid: 126, name: secret-thread-name  >>> {TARGET} <<<
backtrace:
    #00 pc 0000000000222222 /private/path/secret-module (secret-symbol)
'''
        stack = filtered_latest(entry(extra=extra), '123')
        self.assertIn('pid=123 tid=125 thread=1.raster\n', stack)
        self.assertIn('pid=123 tid=126 thread=unlisted\n', stack)
        self.assertIn('pc=0000000000222222 module=unlisted\n', stack)
        self.assertNotIn('secret', stack)
        self.assertIsNone(filtered_latest(entry(extra=extra.replace('pid: 123, tid: 125', 'pid: 999, tid: 125')), '123'))

    def test_current_suite_pid_excludes_older_target_crash(self):
        raw = entry(pid=123) + entry(pid=234, timestamp='2026-10-13 12:01:00')
        stack = filtered_latest(raw, '234')
        self.assertIn('entry_time=2026-10-13 12:01:00', stack)
        self.assertIn('pid=234 tid=235', stack)
        self.assertNotIn('pid=123', stack)
        self.assertIsNone(filtered_latest(entry(pid=123), '234'))

    def test_suite_pid_requires_exact_unambiguous_target_header(self):
        header = f'pid: 123, tid: 124, name: 1.ui  >>> {TARGET} <<<'
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'suite.log'
            for prefix in ('', 'F/DEBUG   ( 999): ', 'F/DEBUG (999): '):
                path.write_text(prefix + header + '\n')
                self.assertEqual(suite_crash_pid(path), '123')
            for text in ('I/flutter (123): normal app output\n',
                         header.replace(TARGET, TARGET + ':worker'),
                         'I/flutter (123): payload ' + header,
                         header + '\n' + header.replace('pid: 123', 'pid: 234')):
                path.write_text(text)
                self.assertIsNone(suite_crash_pid(path))

    def test_unknown_suite_pid_does_not_read_dropbox_or_keep_stale_stack(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            suite_log = evidence / 'suite.log'
            suite_log.write_text('I/flutter (123): no native crash header\n')
            (evidence / 'stack.txt').write_text('old target crash')
            def forbidden_read(*args, **kwargs):
                self.fail('Unknown crash PID must not request DropBox history')
            collect(evidence, suite_log, run=forbidden_read)
            self.assertEqual((evidence / 'status.txt').read_text(),
                             'status=unavailable\nreason=no_unique_suite_crash_pid\n')
            self.assertFalse((evidence / 'stack.txt').exists())

    def test_other_tag_boundary_cannot_contribute_frames(self):
        raw = entry() + '''========================================
2026-10-13 12:02:00 data_app_crash (text, 300 bytes)
backtrace:
    #00 pc 0000000000009999 /system/libforeign.so
'''
        self.assertEqual(filtered_latest(raw, '123'), filtered_latest(entry(), '123'))

    def test_unavailable_and_failed_adb_never_persist_raw_data_or_retry(self):
        for raw, code, reason in [(b'Permission Denial secret-output', 1, 'adb_failed'),
                                  (entry().encode(), 1, 'adb_failed'),
                                  (b"Can't find service: dropbox secret-output", 0, 'no_matching_target_stack'),
                                  (b'', 0, 'no_matching_target_stack')]:
            with self.subTest(code=code, reason=reason):
                files = self.run_case(stdout=raw, returncode=code)
                self.assertEqual(files, {'status.txt': f'status=unavailable\nreason={reason}\n'})

    def test_timeout_partial_output_and_os_errors_are_not_saved(self):
        for error, reason in [(subprocess.TimeoutExpired(COMMAND, 4, output=entry().encode(), stderr=b'secret'), 'adb_timeout'),
                              (FileNotFoundError('secret executable path'), 'read_or_write_failed')]:
            with self.subTest(reason=reason):
                files = self.run_case(error=error)
                self.assertEqual(files, {'status.txt': f'status=unavailable\nreason={reason}\n'})

    def test_oversized_output_is_not_parsed_or_persisted(self):
        files = self.run_case(error=OutputLimitExceeded())
        self.assertEqual(files, {'status.txt': 'status=unavailable\nreason=output_limit\n'})

    def test_unavailable_read_clears_stale_generated_stack(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            (evidence / 'stack.txt').write_text('old stack')
            def failed(command, **kwargs):
                return subprocess.CompletedProcess(command, 1, b'', b'')
            suite_log = evidence / 'suite.log'
            suite_log.write_text(f'pid: 123, tid: 124, name: 1.ui  >>> {TARGET} <<<\n')
            collect(evidence, suite_log, run=failed)
            self.assertFalse((evidence / 'stack.txt').exists())
            self.assertEqual((evidence / 'status.txt').read_text(),
                             'status=unavailable\nreason=adb_failed\n')

    def test_stream_reader_returns_bounded_stdout_without_stderr(self):
        result = read_bounded([sys.executable, '-c',
                               'import sys; print("metadata"); sys.stderr.write("secret"); sys.exit(7)'],
                              timeout=2, limit=32)
        self.assertEqual(result.returncode, 7)
        self.assertEqual(result.stdout, b'metadata\n')
        self.assertIsNone(result.stderr)

    def test_stream_reader_stops_at_byte_limit(self):
        with self.assertRaises(OutputLimitExceeded):
            read_bounded([sys.executable, '-c', 'import sys; sys.stdout.write("x" * 1000000)'],
                         timeout=2, limit=32)

    def test_stream_reader_timeout_discards_partial_output(self):
        started = time.monotonic()
        with self.assertRaises(subprocess.TimeoutExpired) as error:
            read_bounded([sys.executable, '-c', 'import time; print("secret", flush=True); time.sleep(10)'],
                         timeout=0.1, limit=32)
        self.assertIsNone(error.exception.output)
        self.assertLess(time.monotonic() - started, 2)


if __name__ == '__main__':
    unittest.main()
