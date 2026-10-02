import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import kvm_once_20261002 as trial

ACL = 'user::rw-\ngroup::rw-\nother::---\n'


class GuardTests(unittest.TestCase):
    def setUp(self):
        self.env = {'GITHUB_ACTIONS': 'true', 'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': trial.BRANCH,
                    'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_RUN_NUMBER': '1', 'GITHUB_RUN_ID': '123',
                    'GITHUB_SHA': 'b' * 40}
        self.event = {'before': trial.BASE, 'after': 'b' * 40, 'repository': {'full_name': 'Live-yum/iotools'}}

    def test_only_designated_first_run(self):
        trial.check_context(self.env, self.event)
        for key, value in [('GITHUB_ACTIONS', 'false'), ('GITHUB_EVENT_NAME', 'workflow_dispatch'),
                           ('GITHUB_REF', 'refs/heads/main'), ('GITHUB_RUN_ATTEMPT', '2'),
                           ('GITHUB_RUN_NUMBER', '2'), ('GITHUB_RUN_ID', ''), ('GITHUB_SHA', 'x')]:
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                trial.check_context({**self.env, key: value}, self.event)

    def test_wrong_or_repeated_source_rejected(self):
        for changes in [{'before': '0' * 40}, {'before': 'b' * 40}, {'forced': True},
                        {'deleted': True}, {'repository': {'full_name': 'someone/else'}}]:
            with self.subTest(changes=changes), self.assertRaises(RuntimeError):
                trial.check_context(self.env, {**self.event, **changes})

    def test_no_mask_broadening(self):
        for text in ['user::rw-\ngroup::r--\nother::---\n', ACL + 'mask::r--\n']:
            with self.subTest(text=text), self.assertRaises(RuntimeError):
                trial.planned_acl(text, 1001)

    def test_other_acl_entries_preserved(self):
        before = ACL + 'user:77:r--\nmask::rw-\n'
        desired, mask = trial.planned_acl(before, 1001)
        self.assertEqual(mask, 'rw-')
        self.assertEqual(desired['user:77'], 'r--')
        self.assertEqual(desired['other:'], '---')
        self.assertEqual(desired['user:1001'], 'rw-')

    def test_invalid_acl_rejected(self):
        for text in ['', 'default:user::rw-', ACL + 'user::rw-\n', ACL.replace('rw-', 'rwa')]:
            with self.subTest(text=text), self.assertRaises(RuntimeError):
                trial.parse_acl(text)

    def exercise(self, accelerator_ok):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            event_path = root / 'event.json'
            event_path.write_text(json.dumps(self.event))
            emulator = root / 'sdk/emulator/emulator'
            emulator.parent.mkdir(parents=True)
            emulator.touch()
            env = {**self.env, 'GITHUB_EVENT_PATH': str(event_path), 'ANDROID_HOME': str(root / 'sdk'),
                   'GITHUB_ENV': str(root / 'env')}
            state, evidence = root / 'state.json', root / 'evidence.json'
            current = [ACL]
            commands = []

            def fake_run(command):
                commands.append(command)
                if command[:3] == ['git', 'diff', '--name-only']:
                    return '\n'.join(sorted(trial.ALLOWED_FILES))
                if command[:2] == ['git', 'rev-parse']:
                    return self.env['GITHUB_SHA']
                if command[0] == 'getfacl':
                    return current[0]
                if command[:3] == ['sudo', '-n', 'setfacl']:
                    if '-m' in command:
                        self.assertIn('--no-mask', command)
                        self.assertEqual(command[command.index('-m') + 1], 'u:1001:rw,m::rw-')
                        current[0] = ACL + 'user:1001:rw-\nmask::rw-\n'
                    else:
                        current[0] = Path(command[command.index('--set-file') + 1]).read_text()
                    return ''
                if command[-1] == '-accel-check':
                    if not accelerator_ok:
                        raise RuntimeError('Accelerator verification failed')
                    return 'accel:\n0\nKVM is installed and usable.\n'
                self.fail(f'Unexpected command: {command}')

            identity = {'uid': 0, 'gid': 108, 'rdev': 232, 'inode': 1, 'mode': 0o660}
            with patch.dict(os.environ, env, clear=True), patch.object(trial, 'run', fake_run), \
                 patch.object(trial, 'identity', return_value=identity), \
                 patch.object(trial.platform, 'system', return_value='Linux'), \
                 patch.object(trial.os, 'getuid', return_value=1001), \
                 patch.object(trial.os, 'access', return_value=True):
                if accelerator_ok:
                    trial.grant(state, evidence)
                    self.assertIn('ANDROID_TEST_API=29', (root / 'env').read_text())
                    self.assertIn('ANDROID_TEST_TARGET=default', (root / 'env').read_text())
                else:
                    with self.assertRaises(RuntimeError):
                        trial.grant(state, evidence)
                    self.assertFalse((root / 'env').exists())
                self.assertNotEqual(current[0], ACL)
                trial.restore(state, evidence)
                self.assertEqual(current[0], ACL)
                self.assertTrue(json.loads(evidence.read_text())['restored'])
                self.assertEqual(json.loads(evidence.read_text())['before_sha256'], trial.digest(ACL))
            self.assertFalse(any('chmod' in ' '.join(c) or 'udev' in ' '.join(c) for c in commands))

    def test_grant_and_verified_restore(self):
        self.exercise(True)

    def test_failure_after_grant_still_restores(self):
        self.exercise(False)

    def test_cleanup_when_grant_never_started(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            with patch.object(trial, 'run') as runner:
                trial.restore(root / 'missing.json', root / 'report.json')
                runner.assert_not_called()
                self.assertEqual(json.loads((root / 'report.json').read_text())['status'], 'not_granted')

    def test_cleanup_identity_guard(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            state = root / 'state.json'
            state.write_text(json.dumps({'run_id': 'other'}))
            with patch.dict(os.environ, {'GITHUB_RUN_ID': '123'}), patch.object(trial, 'run') as runner:
                with self.assertRaises(RuntimeError):
                    trial.restore(state, root / 'report.json')
                runner.assert_not_called()

    def test_workflow_keeps_baseline_tests_and_security_scope(self):
        root = Path(__file__).resolve().parents[2]
        original = (root / '.github/workflows/android.yml').read_text()
        controlled = (root / '.github/workflows/android-kvm-once-20261002.yml').read_text()
        self.assertIn('github.run_attempt == 1 && github.run_number == 1', controlled)
        self.assertNotIn('workflow_dispatch:', controlled)
        self.assertIn("branches: ['trial/kvm-once-20261002-0513']", controlled)
        self.assertIn('fetch-depth: 2', controlled)
        marker = '      - name: Real emulator UI, protocol, editing and lifecycle tests\n'
        expected = original[original.index(marker):original.index('      - uses: actions/upload-artifact@v4\n')]
        expected = expected.replace('reactivecircus/android-emulator-runner@v2',
                                    'reactivecircus/android-emulator-runner@a421e43855164a8197daf9d8d40fe71c6996bb0d')
        self.assertIn(expected, controlled)
        self.assertIn('Restore and verify the original KVM ACL even if testing fails\n        if: always()', controlled)
        self.assertEqual(controlled.count('kvm_once_20261002.py grant'), 1)
        self.assertIn('timeout-minutes: 60', controlled)


if __name__ == '__main__':
    unittest.main(verbosity=2)
