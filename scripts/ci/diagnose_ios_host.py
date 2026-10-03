#!/usr/bin/env python3
"""Read-only diagnostics for the fixed CI simulator host; never an acceptance gate."""
import json
import os
from pathlib import Path
import subprocess
import time

DEVELOPER_DIR = '/Applications/Xcode_26.2.app/Contents/Developer'
OUTPUT = Path('platform-evidence/ios-host-diagnostics')
# Keep the discovery commands and their 30-second bounds. Do not install,
# create, boot, reset or delete simulators, restart services, or select Xcode.
COMMANDS = (
    ('version', ['xcrun', 'xcodebuild', '-version']),
    ('sdk', ['xcrun', '--sdk', 'iphonesimulator', '--show-sdk-version']),
    ('simctl-path', ['xcrun', '--find', 'simctl']),
    ('xcdevice', ['xcrun', 'xcdevice', 'list', '--timeout', '10']),
    ('devices', ['xcrun', 'simctl', 'list', 'devices', 'available', '--json']),
    ('runtimes', ['xcrun', 'simctl', 'list', 'runtimes', '--json']),
    # Compare the first device inventory with the same read after CoreSimulator
    # discovery. Keep both observations: a later healthy list cannot erase an
    # early empty result or timeout, and this is never an acceptance fallback.
    ('xcdevice-after-discovery', ['xcrun', 'xcdevice', 'list', '--timeout', '10']),
    ('processes', ['pgrep', '-fl', 'CoreSimulator|simdiskimaged']),
    ('memory', ['vm_stat']),
    ('disk', ['df', '-h', '/Library/Developer/CoreSimulator']),
    ('service-log', ['/usr/bin/log', 'show', '--last', '2m', '--style', 'compact',
                     '--predicate', 'process CONTAINS "CoreSimulator" OR process == "simdiskimaged"']),
)


def main():
    OUTPUT.mkdir(parents=True, exist_ok=True)
    environment = dict(os.environ, DEVELOPER_DIR=DEVELOPER_DIR)
    report = {'source_sha': os.environ.get('IOTOOLS_SHA', 'local'),
              'scope': 'read-only host diagnostics; no app or simulator acceptance',
              'developer_dir': DEVELOPER_DIR, 'queries': []}
    for name, command in COMMANDS:
        start = time.monotonic()
        observed = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        timed_out = False
        # Write directly to evidence files rather than accumulating potentially
        # large service logs in memory. Every process has the original 30s bound.
        stdout_path = OUTPUT / f'{name}.txt'
        stderr_path = OUTPUT / f'{name}-stderr.txt'
        with stdout_path.open('wb') as stdout, stderr_path.open('wb') as stderr:
            try:
                code = subprocess.run(command, env=environment, stdout=stdout,
                                      stderr=stderr, timeout=30, check=False).returncode
            except subprocess.TimeoutExpired:
                code = None
                timed_out = True
            except OSError as error:
                stderr.write(str(error).encode())
                code = None
        report['queries'].append({'name': name, 'command': command,
                                  'observed_at': observed, 'elapsed_seconds': round(time.monotonic()-start, 3),
                                  'exit_code': code, 'timed_out': timed_out,
                                  'stdout': stdout_path.name, 'stderr': stderr_path.name,
                                  'stdout_bytes': stdout_path.stat().st_size,
                                  'stderr_bytes': stderr_path.stat().st_size})
        (OUTPUT / 'report.json').write_text(json.dumps(report, indent=2)+'\n')
    # Diagnostic collection succeeding does not mean the simulator is healthy.
    print(json.dumps(report))


if __name__ == '__main__':
    main()
