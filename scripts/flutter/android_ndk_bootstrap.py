#!/usr/bin/env python3
"""Validate the CI NDK before Gradle can auto-install it; provision once if absent."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess

NDK_VERSION = '28.1.13356709'
TOOLCHAIN = Path('toolchains/llvm/prebuilt/linux-x86_64/bin')
REQUIRED_TOOLS = (
    'aarch64-linux-android26-clang', 'x86_64-linux-android26-clang',
    'clang', 'clang++', 'ld.lld', 'llvm-ar', 'llvm-ranlib', 'llvm-strip',
    'llvm-readelf',
)


def validate(ndk):
    result = {'path': str(ndk), 'revision': None, 'errors': []}
    try:
        properties = (ndk / 'source.properties').read_text()
        result['source_properties'] = properties
        revisions = re.findall(r'^\s*Pkg\.Revision\s*=\s*([^\r\n]+)', properties, re.M)
        if len(revisions) == 1:
            result['revision'] = revisions[0].strip()
        if result['revision'] != NDK_VERSION:
            result['errors'].append(f'Expected exactly one Pkg.Revision={NDK_VERSION}')
    except (OSError, UnicodeError) as error:
        result['errors'].append(f'Cannot read source.properties: {error}')
    for name in REQUIRED_TOOLS:
        tool = ndk / TOOLCHAIN / name
        if not tool.is_file() or not os.access(tool, os.X_OK):
            result['errors'].append(f'Missing executable: {tool}')
    return result


def install(sdkmanager, sdk, timeout, log_path):
    command = [str(sdkmanager), f'--sdk_root={sdk}', f'ndk;{NDK_VERSION}']
    result = {'command': command, 'timeout_seconds': timeout, 'exit_code': None}
    with log_path.open('w') as log:
        log.write('command=' + json.dumps(command) + '\n')
        log.flush()
        try:
            # EOF never accepts a new license. Kill the process group on timeout
            # so a launched Java downloader cannot outlive the bounded attempt.
            process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            try:
                result['exit_code'] = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                result['exit_code'] = 124
                result['timed_out'] = True
            result['process_returncode'] = process.returncode
        except OSError as error:
            result['exit_code'] = 1
            result['error'] = str(error)
        log.write(f"\nsdkmanager_exit={result['exit_code']}\n")
    return result


def bootstrap(sdk, github_env, evidence, timeout):
    evidence.mkdir(parents=True, exist_ok=True)
    ndk = sdk / 'ndk' / NDK_VERSION
    report = {'expected_revision': NDK_VERSION, 'installation': None,
              'initial_validation': validate(ndk), 'status': 'failed'}
    code = 1
    try:
        if report['initial_validation']['errors']:
            # Never erase/repair an existing partial or conflicting installation.
            if ndk.exists() or ndk.is_symlink():
                raise ValueError('Existing pinned NDK is invalid; left untouched')
            license_file = sdk / 'licenses/android-sdk-license'
            if not license_file.is_file() or not license_file.read_text().strip():
                raise ValueError('An already accepted Android SDK license is required')
            sdkmanager = sdk / 'cmdline-tools/latest/bin/sdkmanager'
            if not sdkmanager.is_file() or not os.access(sdkmanager, os.X_OK):
                raise ValueError(f'Installed SDK command-line tools missing: {sdkmanager}')
            report['installation'] = install(sdkmanager, sdk, timeout, evidence / 'sdkmanager.log')
            # Record even a failed download's revision/partial tree before exiting.
            report['final_validation'] = validate(ndk)
            if report['installation']['exit_code'] != 0:
                code = report['installation']['exit_code']
                raise ValueError(f'sdkmanager failed with exit={code}')
            if report['final_validation']['errors']:
                raise ValueError('Provisioned NDK failed validation')
        else:
            report['final_validation'] = report['initial_validation']
        # One validated version is shared by native Go and the Gradle ndkVersion.
        with github_env.open('a') as environment:
            environment.write(f'ANDROID_NDK_HOME={ndk}\n')
        report['status'] = 'validated'
        code = 0
    except (OSError, UnicodeError, ValueError) as error:
        report['error'] = str(error)
    finally:
        (evidence / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report, indent=2), flush=True)
    return code if code >= 0 else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sdk-root', required=True, type=Path)
    parser.add_argument('--github-env', required=True, type=Path)
    parser.add_argument('--evidence-dir', type=Path, default=Path('android-evidence/ndk-bootstrap'))
    parser.add_argument('--install-timeout', type=float, default=600)
    args = parser.parse_args()
    if not 0 < args.install_timeout <= 600:
        parser.error('--install-timeout must be positive and at most 600 seconds')
    return bootstrap(args.sdk_root.resolve(), args.github_env, args.evidence_dir, args.install_timeout)


if __name__ == '__main__':
    raise SystemExit(main())
