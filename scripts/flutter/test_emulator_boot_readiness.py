#!/usr/bin/env python3
"""Hermetic framework-restart/readiness contracts; no device or network."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from emulator_boot_readiness import FLAGS, MAX_OUTPUT_BYTES, ReadinessError, ensure_ready, framework_flags


def state(**changes):
    sample = {'pid_before': '2934\n', 'pid_after': '2934\n', 'sdk': '29', 'boot': '1',
              'flags': dict(FLAGS), 'package': 'package:/system/framework/framework-res.apk\n'}
    sample.update(changes)
    return sample


class Device:
    def __init__(self, samples, *, duration=1):
        self.samples = samples
        self.duration = duration
        self.time = 0
        self.calls = []

    def now(self):
        return self.time

    def sleep(self, duration):
        self.time += duration

    def run(self, command, *, timeout, limit):
        self.calls.append((command, timeout, limit, self.time))
        sample = self.samples[min((len(self.calls) - 1) // 5, len(self.samples) - 1)]
        part = (len(self.calls) - 1) % 5
        self.time += min(timeout, self.duration)
        if self.duration > timeout or sample.get('timeout') == part:
            raise subprocess.TimeoutExpired(command, timeout)
        if part == 0:
            raw = sample['pid_before']
        elif part == 1:
            raw = (f"[ro.build.version.sdk]: [{sample['sdk']}]\n"
                   f"[sys.boot_completed]: [{sample['boot']}]\n"
                   '[private.property]: [private-getprop-payload]\n')
        elif part == 2:
            raw = ' '.join(f'{name}={str(value).lower()}' for name, value in sample['flags'].items())
            raw += '\nprivate-dumpsys-payload\n'
        elif part == 3:
            raw = sample['package']
        else:
            raw = sample['pid_after']
        raw += sample.get('extra', {}).get(part, '')
        code = 1 if sample.get('failed') == part else 0
        return subprocess.CompletedProcess(command, code, raw.encode())


class BootReadinessTests(unittest.TestCase):
    def check(self, device, *, api=29, ready=False, serial='emulator-5554'):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / 'device-ready.log'
            if ready:
                ensure_ready(serial, api, evidence, run=device.run, now=device.now, sleep=device.sleep)
            else:
                with self.assertRaises(ReadinessError):
                    ensure_ready(serial, api, evidence, run=device.run, now=device.now, sleep=device.sleep)
            raw = evidence.read_text()
            self.assertNotIn('private-', raw)
            self.assertNotIn('framework-res.apk', raw)
            self.assertLessEqual(device.time, 60)
            for command, timeout, limit, began in device.calls:
                self.assertEqual(command[:3], ('adb', '-s', serial))
                self.assertGreater(timeout, 0)
                self.assertLessEqual(timeout, min(5, 60 - began))
                self.assertEqual(limit, MAX_OUTPUT_BYTES)
            return [json.loads(line) for line in raw.splitlines()]

    def test_both_workflow_apis_require_three_current_framework_samples(self):
        for api in (29, 35):
            with self.subTest(api=api):
                device = Device([state(sdk=str(api))])
                records = self.check(device, api=api, ready=True)
                self.assertEqual([row['consecutive'] for row in records[:-1]], [1, 2, 3])
                self.assertEqual(records[-1]['status'], 'ready')
                self.assertEqual(len(device.calls), 15)
                self.assertEqual(device.calls[1][0][3:], ('shell', 'getprop'))
                self.assertEqual(device.calls[2][0][3:], ('shell', 'dumpsys', 'activity', 'processes'))

    def test_residual_boot_one_and_available_package_do_not_mean_restarted_framework_is_ready(self):
        # 8e3c96e: SDK 29/package calls pass while system_server 2934 is still
        # booting after 1951 died; the old scalar-property gate accepts this.
        partial = state(flags=FLAGS | {'mBooted': False, 'mBooting': True, 'mBootAnimationComplete': False})
        self.assertTrue(int(partial['sdk']) >= 26 and partial['package'].startswith('package:'))
        records = self.check(Device([partial]))
        self.assertTrue(all(row['consecutive'] == 0 for row in records[:-1]))
        self.assertEqual(records[-1]['status'], 'failed')

    def test_restart_between_samples_resets_stability_count(self):
        old = state(pid_before='1951', pid_after='1951')
        records = self.check(Device([old, old, state(), state(), state()]), ready=True)
        self.assertEqual([row['consecutive'] for row in records[:-1]], [1, 2, 1, 2, 3])

    def test_restart_within_a_sample_cannot_mix_two_instances(self):
        records = self.check(Device([state(), state(pid_before='1951'), state(), state(), state()]), ready=True)
        self.assertEqual([row['consecutive'] for row in records[:-1]], [1, 0, 1, 2, 3])

    def test_each_unknown_or_wrong_boot_flag_fails_closed(self):
        for name, expected in FLAGS.items():
            for value in (None, not expected):
                with self.subTest(flag=name, value=value):
                    flags = dict(FLAGS)
                    if value is None:
                        del flags[name]
                    else:
                        flags[name] = value
                    self.check(Device([state(flags=flags)]))

    def test_unknown_boolean_and_duplicate_flags_are_not_guessed(self):
        for raw in ('mBooted=unknown', 'mBooted=true mBooted=false', 'mBooted=true mBooted=true'):
            self.assertIsNone(framework_flags(raw)['mBooted'])
        self.check(Device([state(extra={2: '\nmBooted=true\n'})]))

    def test_api_mismatch_malformed_or_duplicate_property_fails_closed(self):
        for sdk in ('1', '26', '35', '029', 'unknown', ''):
            with self.subTest(sdk=sdk):
                self.check(Device([state(sdk=sdk)]))
        self.check(Device([state(extra={1: '[ro.build.version.sdk]: [29]\n'})]))

    def test_boot_property_pid_and_package_must_be_unambiguous(self):
        for change in ({'boot': '0'}, {'boot': ''}, {'pid_before': ''},
                       {'pid_after': '2934 2935'}, {'package': 'package:/wrong.apk'},
                       {'package': 'package:/system/framework/framework-res.apk\nerror'}):
            with self.subTest(change=change):
                self.check(Device([state(**change)]))

    def test_failed_or_timed_out_command_cannot_pass_using_its_stdout(self):
        for part in range(5):
            for failure in ('failed', 'timeout'):
                with self.subTest(part=part, failure=failure):
                    self.check(Device([state(**{failure: part})]))

    def test_interrupted_valid_streak_requires_three_new_samples(self):
        records = self.check(Device([state(), state(failed=1), state(), state(), state()]), ready=True)
        self.assertEqual([row['consecutive'] for row in records[:-1]], [1, 0, 1, 2, 3])

    def test_deadline_clips_each_read_and_never_extends_for_cleanup(self):
        device = Device([state()], duration=10)
        records = self.check(device)
        self.assertEqual(device.time, 60)
        self.assertEqual(records[-1]['reason'], 'deadline')
        self.assertTrue(any(timeout < 5 for _, timeout, _, _ in device.calls))

    def test_missing_device_selection_or_unsupported_api_never_probes(self):
        for serial, api in (('', 29), ('device-private-id', 29), ('emulator-5554', 26)):
            device = Device([state()])
            self.check(device, serial=serial, api=api)
            self.assertEqual(device.calls, [])


if __name__ == '__main__':
    unittest.main()
