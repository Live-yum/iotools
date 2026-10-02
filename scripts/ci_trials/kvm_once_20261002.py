#!/usr/bin/env python3
"""One owner-approved ephemeral-runner KVM ACL trial; never a default grant."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import stat
import subprocess

BASE = '8db14221104175fd265b4d08f51b905f0bc999a0'
BRANCH = 'refs/heads/trial/kvm-once-20261002-0513'
TRIGGER_PARENT = 'd85f2cc33bf389436e0074bf682f79027ed8783a'
TRIAL_RUN_NUMBER = '3'  # Run 1 failed setup before emulator execution; ACL was verified restored.
DEVICE = Path('/dev/kvm')
ALLOWED_FILES = {
    '.github/workflows/android-kvm-once-20261002.yml',
    'scripts/ci_trials/kvm_once_20261002.py',
    'scripts/ci_trials/test_kvm_once_20261002.py',
}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(args):
    result = subprocess.run(args, check=False, text=True, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError(f'Command {args[0]} exited {result.returncode}: {(result.stderr or result.stdout)[-6000:]}')
    return result.stdout


def check_context(env, event):
    require(env.get('GITHUB_ACTIONS') == 'true', 'Only the designated GitHub runner is permitted')
    require(env.get('GITHUB_EVENT_NAME') == 'push', 'Only the one controlled push is permitted')
    require(env.get('GITHUB_REF') == BRANCH, 'Wrong trial branch')
    require(env.get('GITHUB_RUN_ATTEMPT') == '1', 'Retries require new owner approval')
    require(env.get('GITHUB_RUN_NUMBER') == TRIAL_RUN_NUMBER, 'Only this single setup-recovery run may grant access')
    require(event.get('before') == TRIGGER_PARENT, 'Trial must start from the approved baseline')
    require(event.get('after') == env.get('GITHUB_SHA'), 'Event source identity mismatch')
    require(not event.get('deleted', False) and not event.get('forced', False), 'Non-force push required')
    require(event.get('repository', {}).get('full_name') == 'Live-yum/iotools', 'Wrong repository')
    require(bool(env.get('GITHUB_RUN_ID', '').isdigit()), 'Missing run identity')


def parse_acl(text):
    result = {}
    for raw in text.splitlines():
        line = raw.split('#', 1)[0].strip()
        if not line:
            continue
        fields = line.split(':')
        require(len(fields) == 3 and fields[0] in {'user', 'group', 'mask', 'other'}, 'Unsupported ACL entry')
        key = ':'.join(fields[:2])
        permissions = fields[2]
        require(len(permissions) == 3 and all(c in (letter, '-') for c, letter in zip(permissions, 'rwx')), 'Invalid ACL permissions')
        require(key not in result, 'Duplicate ACL entry')
        result[key] = permissions
    require({'user:', 'group:', 'other:'} <= result.keys(), 'Incomplete ACL')
    return result


def planned_acl(text, uid):
    acl = parse_acl(text)
    mask = acl.get('mask:', acl['group:'])
    require(mask[:2] == 'rw', 'Existing ACL mask must already permit rw; no broadening allowed')
    desired = dict(acl)
    desired[f'user:{uid}'] = 'rw-'
    desired['mask:'] = mask
    return desired, mask


def identity():
    info = DEVICE.lstat()
    require(stat.S_ISCHR(info.st_mode), 'KVM must be an existing character device, not a symlink')
    return {'uid': info.st_uid, 'gid': info.st_gid, 'rdev': info.st_rdev, 'inode': info.st_ino,
            'mode': stat.S_IMODE(info.st_mode)}


def acl_text():
    return run(['getfacl', '--absolute-names', '--omit-header', '--numeric', str(DEVICE)])


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')


def verified_emulator():
    emulator = Path(os.environ['ANDROID_HOME']) / 'emulator' / 'emulator'
    require(emulator.is_file(), 'Install the official Android emulator before granting permissions')
    version = run([str(emulator), '-no-window', '-version'])
    require('37.2.12' in version and '16428233' in version, 'Emulator differs from the controlled baseline')
    return emulator


def preflight(state, evidence):
    check_context(os.environ, json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text()))
    require(platform.system() == 'Linux' and os.getuid() != 0, 'Expected unprivileged Linux runner')
    verified_emulator()
    info = identity()
    before = acl_text()
    planned_acl(before, os.getuid())
    run(['sudo', '-n', 'true'])
    write_json(evidence, {'status': 'preflight_passed', 'permission_mutation_started': False,
                         'device': info, 'runner_uid': os.getuid(), 'acl_sha256': digest(before)})


def grant(state, evidence):
    check_context(os.environ, json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text()))
    require(platform.system() == 'Linux' and os.getuid() != 0, 'Expected unprivileged Linux runner')
    changed = set(run(['git', 'diff', '--name-only', BASE, 'HEAD']).splitlines())
    require(changed == ALLOWED_FILES, 'Runtime or unrelated source changed in this controlled experiment')
    require(run(['git', 'rev-parse', 'HEAD']).strip() == os.environ['GITHUB_SHA'], 'Checkout differs from requested source')
    emulator = verified_emulator()  # All installation/version checks precede the ACL mutation.
    before_device = identity()
    before = acl_text()
    desired, mask = planned_acl(before, os.getuid())
    require(not state.exists() and not state.is_symlink(), 'Trial state already exists')
    state.parent.mkdir(parents=True, exist_ok=True)
    record = {'run_id': os.environ['GITHUB_RUN_ID'], 'source_sha': os.environ['GITHUB_SHA'],
              'baseline_sha': BASE, 'runner_uid': os.getuid(), 'device': before_device,
              'acl_before': before, 'before_sha256': digest(before), 'restored': False}
    fd = os.open(state, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(record, output)
    # The snapshot is durable BEFORE the only permission mutation; always-cleanup
    # restores it even if setfacl or the following accelerator check fails.
    run(['sudo', '-n', 'setfacl', '--no-mask', '-m', f'u:{os.getuid()}:rw,m::{mask}', str(DEVICE)])
    require(identity() == before_device, 'KVM device identity/owner/mode changed unexpectedly')
    after = acl_text()
    require(parse_acl(after) == desired, 'ACL grant changed entries beyond the current runner UID')
    require(os.access(DEVICE, os.R_OK | os.W_OK), 'Runner still cannot access KVM')
    acceleration = run([str(emulator), '-no-window', '-accel-check'])
    require('KVM' in acceleration and 'usable' in acceleration.lower(), 'KVM usability check did not pass')
    write_json(evidence, {'status': 'granted', 'run_id': record['run_id'], 'source_sha': record['source_sha'],
                         'runner_uid': record['runner_uid'], 'before_sha256': record['before_sha256'],
                         'after_sha256': digest(after), 'accelerator_check': acceleration,
                         'scope': 'one ephemeral runner UID; existing ACL mask unchanged', 'restored': False})
    with Path(os.environ['GITHUB_ENV']).open('a') as output:
        output.write('ANDROID_EMULATOR_ACCEL=-accel on\nANDROID_HW_DISABLE=false\nANDROID_TEST_API=29\nANDROID_TEST_TARGET=default\n')


def restore(state, evidence):
    if not state.exists():
        write_json(evidence, {'status': 'not_granted', 'restored': True, 'permission_mutation_started': False})
        return
    record = json.loads(state.read_text())
    require(record['run_id'] == os.environ.get('GITHUB_RUN_ID'), 'Cleanup belongs to a different run')
    require(record['runner_uid'] == os.getuid(), 'Cleanup runner identity changed')
    require(identity() == record['device'], 'KVM identity changed; refusing to touch another device')
    require(digest(record['acl_before']) == record['before_sha256'], 'Saved ACL digest mismatch')
    backup = state.with_suffix('.acl')
    fd = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as output:
        output.write(record['acl_before'])
    run(['sudo', '-n', 'setfacl', '--no-mask', '--set-file', str(backup), str(DEVICE)])
    after = acl_text()
    require(parse_acl(after) == parse_acl(record['acl_before']), 'KVM ACL restoration verification failed')
    require(identity() == record['device'], 'Device mode/owner changed during restoration')
    record.update(restored=True, status='restored', after_restore_sha256=digest(after))
    write_json(state, record)
    write_json(evidence, {k: v for k, v in record.items() if k != 'acl_before'})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['preflight', 'grant', 'restore'])
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    {'preflight': preflight, 'grant': grant, 'restore': restore}[args.action](args.state, args.evidence)


if __name__ == '__main__':
    main()
