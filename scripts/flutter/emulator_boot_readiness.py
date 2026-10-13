#!/usr/bin/env python3
"""Require a stable, booted framework before ddmlib discovers the test device.

Android runtime restarts retain sys.boot_completed=1. Check the current AMS
instance too, with its system_server PID on both sides of the sample. Raw
getprop/dumpsys output stays bounded in memory; evidence contains only typed
allowlisted state and timings. This polls prerequisites, never retries tests.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

from native_crash_evidence import OutputLimitExceeded, read_bounded

TOTAL_TIMEOUT = 60
READ_TIMEOUT = 5
MAX_OUTPUT_BYTES = 1024 * 1024
REQUIRED_SAMPLES = 3
FLAGS = {
    'mProcessesReady': True,
    'mSystemReady': True,
    'mBooted': True,
    'mBooting': False,
    'mCallFinishBooting': False,
    'mBootAnimationComplete': True,
}
# These fields/"processes" routing are present in both workflow images:
# frameworks/base android-10.0.0_r47 AMS.java:10855 and
# android-15.0.0_r1 AMS.java:11474. Unknown future output fails closed.


class ReadinessError(RuntimeError):
    pass


def unique_property(raw, name):
    values = re.findall(r'^\[' + re.escape(name) + r'\]: \[([^\r\n]*)\]\r?$', raw, re.M)
    return values[0] if len(values) == 1 else None


def framework_flags(raw):
    result = {}
    for name in FLAGS:
        values = re.findall(r'\b' + name + r'=([^\s]+)', raw)
        result[name] = values[0] == 'true' if len(values) == 1 and values[0] in ('true', 'false') else None
    return result


def single_pid(raw):
    value = raw.strip()
    return int(value) if re.fullmatch(r'[1-9][0-9]{0,9}', value) else None


def ensure_ready(serial, expected_api, evidence, *, run=read_bounded,
                 now=time.monotonic, sleep=time.sleep):
    start = now()
    deadline = start + TOTAL_TIMEOUT
    consecutive = 0
    previous_pid = None
    evidence.parent.mkdir(parents=True, exist_ok=True)
    with evidence.open('w') as log:
        def record(data):
            log.write(json.dumps(data, sort_keys=True) + '\n')
            log.flush()

        if not re.fullmatch(r'emulator-[0-9]+', serial) or expected_api not in (29, 35):
            record({'status': 'failed', 'reason': 'unsupported_device_or_api'})
            raise ReadinessError('Unsupported emulator selector or API')
        attempt = 0
        while now() < deadline:
            attempt += 1
            sample = {'attempt': attempt, 'commands': {}, 'expected_api': expected_api}

            def probe(label, *args):
                remaining = deadline - now()
                if remaining <= 0:
                    sample['commands'][label] = {'status': 'deadline', 'elapsed_ms': 0}
                    return None
                began = now()
                status = 'ok'
                raw = None
                try:
                    result = run(('adb', '-s', serial, *args),
                                 timeout=min(READ_TIMEOUT, remaining), limit=MAX_OUTPUT_BYTES)
                    if result.returncode:
                        status = 'command_failed'
                    else:
                        raw = result.stdout.decode('utf-8', errors='strict')
                except subprocess.TimeoutExpired:
                    status = 'timeout'
                except OutputLimitExceeded:
                    status = 'output_limit'
                except (OSError, UnicodeError):
                    status = 'unavailable'
                sample['commands'][label] = {'status': status, 'elapsed_ms': round((now() - began) * 1000)}
                return raw

            before = probe('pid_before', 'shell', 'pidof', 'system_server')
            properties = probe('properties', 'shell', 'getprop')
            activity = probe('framework', 'shell', 'dumpsys', 'activity', 'processes')
            package = probe('package', 'shell', 'cmd', 'package', 'path', 'android')
            after = probe('pid_after', 'shell', 'pidof', 'system_server')
            pid_before, pid_after = single_pid(before or ''), single_pid(after or '')
            sdk = unique_property(properties or '', 'ro.build.version.sdk')
            boot = unique_property(properties or '', 'sys.boot_completed')
            flags = framework_flags(activity or '')
            package_ready = (package or '').strip() == 'package:/system/framework/framework-res.apk'
            valid = (pid_before is not None and pid_before == pid_after
                     and sdk == str(expected_api) and boot == '1'
                     and flags == FLAGS and package_ready and now() < deadline)
            if valid:
                consecutive = consecutive + 1 if pid_after == previous_pid else 1
                previous_pid = pid_after
            else:
                consecutive, previous_pid = 0, None
            sample.update(pid_before=pid_before, pid_after=pid_after,
                          sdk=int(sdk) if sdk is not None and re.fullmatch(r'[0-9]{1,3}', sdk) else None,
                          boot_completed=boot == '1' if boot in ('0', '1') else None,
                          framework=flags, package_ready=package_ready,
                          valid=valid, consecutive=consecutive,
                          elapsed_ms=round((now() - start) * 1000))
            record(sample)
            if consecutive == REQUIRED_SAMPLES:
                record({'status': 'ready', 'samples': consecutive, 'elapsed_ms': round((now() - start) * 1000)})
                return
            sleep(min(.2, max(0, deadline - now())))
        record({'status': 'failed', 'reason': 'deadline', 'elapsed_ms': round((now() - start) * 1000)})
        raise ReadinessError('Android framework did not become consistently ready within 60 seconds')


if __name__ == '__main__':
    try:
        ensure_ready(os.environ.get('ANDROID_SERIAL', ''),
                     int(os.environ.get('ANDROID_TEST_API', '0')), Path(sys.argv[1]))
    except (ReadinessError, ValueError):
        print('Android framework readiness failed; see allowlisted device-ready.log', file=sys.stderr)
        sys.exit(75)
