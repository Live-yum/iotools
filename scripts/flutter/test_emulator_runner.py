#!/usr/bin/env python3
"""Hermetic runner exit/installation checks; fake processes, no Android or network."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest

RUNNER = Path(__file__).with_name('run-emulator-tests.sh')
WORKFLOW = RUNNER.parents[2]/'.github/workflows/android.yml'

class RunnerTests(unittest.TestCase):
    def run_case(self, *, drive=0, opcua=0, stop=0, plugin=0, instrument=0, verification=0, cleanup=0, renderer=0,
                 drive_log='I/flutter ( 123): 16:17 +6: All tests passed!',
                 opcua_log='I/flutter ( 456): 07:20 +2: All tests passed!'):
        with tempfile.TemporaryDirectory(prefix='iotools-runner-') as directory:
            root = Path(directory)
            for name in ('scripts/flutter', 'mobile/android', 'android-dist', 'android-evidence/aot-test', 'bin', 'android-sdk/emulator'):
                (root/name).mkdir(parents=True, exist_ok=True)
            shutil.copyfile(RUNNER, root/'scripts/flutter/run-emulator-tests.sh')
            apk = root/'android-dist/iotools-flutter-universal-fixture-aot-test-signed.apk'
            apk.write_bytes(b'normal-entry-immutable-signed-apk-fixture')
            (root/'android-evidence/aot-test/instrumentation.apk').write_bytes(b'test-apk')
            def script(path, text):
                path.write_text('#!/usr/bin/env bash\nset -eu\n'+text+'\n')
                path.chmod(0o700)
            script(root/'mobile/android/gradlew', 'exit 0')
            script(root/'android-sdk/emulator/emulator', 'printf "emulator %s\\n" "$*" >> "$TRACE"; printf "Android emulator version hermetic\\n"')
            script(root/'bin/flutter', 'printf "drive %s\\n" "$*" >> "$TRACE"; case "$*" in *opcua_workflow_test.dart*) printf "%s\\n" "$OPCUA_LOG"; exit "$OPCUA_RESULT";; *) printf "%s\\n" "$DRIVE_LOG"; exit "$DRIVE_RESULT";; esac')
            script(root/'bin/curl', 'printf "{}\\n"')
            script(root/'bin/go', '''while test "$1" != -o; do shift; done
shift
cat > "$1" <<'FIXTURE'
#!/usr/bin/env bash
printf '{}' > "$2"
trap 'exit 0' TERM
while :; do sleep .1; done
FIXTURE
chmod 700 "$1"''')
            script(root/'bin/adb', '''printf 'adb %s\\n' "$*" >> "$TRACE"
if test "$1" = shell && test "$2" = getprop; then printf '29\\n'; fi
if test "$1" = shell && test "$2" = cmd; then printf 'package:/system/framework/framework-res.apk\\n'; fi
if test "$1" = features; then printf 'cmd,shell_v2\\n'; fi
if test "$1" = shell && test "$2" = dumpsys && test "$3" = SurfaceFlinger; then printf 'GLES: hermetic software renderer\\n'; exit "$RENDERER_RESULT"; fi
if test "$1" = shell && test "$2" = am && test "$3" = force-stop; then exit "$STOP_RESULT"; fi
if test "$1" = install; then test -f "$3"; fi
if test "$1" = shell && test "$2" = am && test "$3" = instrument; then
  printf 'OK (1 test)\\n'
  exit "$INSTRUMENT_RESULT"
fi
if test "$1" = reverse && test "${2:-}" = --remove; then exit "$CLEANUP_RESULT"; fi''')
            script(root/'bin/python3', '''printf 'verify %s\\n' "$*" >> "$TRACE"
if test "$1" = scripts/flutter/verify-mobile.py; then exit "$PLUGIN_RESULT"; fi
if test "$1" = scripts/flutter/verify-aot-device.py; then exit "$VERIFY_RESULT"; fi
exit 99''')
            trace = root/'trace.txt'
            env = os.environ | {'PATH': str(root/'bin')+os.pathsep+os.environ['PATH'], 'RUNNER_TEMP':str(root), 'ANDROID_HOME':str(root/'android-sdk'), 'TRACE':str(trace), 'IOTOOLS_SHA':'a'*40, 'DRIVE_RESULT':str(drive), 'OPCUA_RESULT':str(opcua), 'DRIVE_LOG':drive_log, 'OPCUA_LOG':opcua_log, 'STOP_RESULT':str(stop), 'PLUGIN_RESULT':str(plugin), 'INSTRUMENT_RESULT':str(instrument), 'VERIFY_RESULT':str(verification), 'CLEANUP_RESULT':str(cleanup), 'RENDERER_RESULT':str(renderer)}
            result = subprocess.run(['bash',str(root/'scripts/flutter/run-emulator-tests.sh')], env=env, cwd=root, capture_output=True, timeout=15)
            recorded=trace.read_text()
            self.assertEqual(recorded.count('emulator -version'),1)
            self.assertLess(recorded.index('adb features'),recorded.index('emulator -version'))
            self.assertEqual((root/'android-evidence/emulator-boot-version.txt').read_text(),'Android emulator version hermetic\n')
            self.assertEqual(recorded.count('adb shell dumpsys SurfaceFlinger'),1)
            self.assertLess(recorded.index('adb features'),recorded.index('adb shell dumpsys SurfaceFlinger'))
            renderer_evidence=(root/'android-evidence/emulator-surfaceflinger.txt').read_text()
            self.assertIn('GLES: hermetic software renderer\n',renderer_evidence)
            self.assertIn(f'adb shell dumpsys SurfaceFlinger exit={renderer}\n',renderer_evidence)
            if stop:
                self.assertNotIn('drive --driver=',recorded)
                self.assertNotIn('adb install',recorded)
                self.assertIn('adb reverse --remove tcp:48416',recorded)
                return result.returncode,recorded
            self.assertIn('--target=integration_test/app_test.dart',recorded)
            self.assertIn('--target=integration_test/opcua_workflow_test.dart',recorded)
            self.assertEqual(recorded.count('drive --driver='),2)
            self.assertEqual(recorded.count('adb shell am force-stop'),3)
            self.assertIn('adb install -r android-dist/'+apk.name,recorded)
            self.assertIn('adb install -r android-evidence/aot-test/instrumentation.apk',recorded)
            self.assertIn('-e apk_sha256 '+hashlib.sha256(apk.read_bytes()).hexdigest(),recorded)
            self.assertIn('-e build_sha '+'a'*40,recorded)
            self.assertLess(recorded.index('adb shell am force-stop'),recorded.index('adb install -r android-dist/'))
            self.assertLess(recorded.index('adb install -r android-dist/'),recorded.index('adb shell am instrument'))
            self.assertIn('adb reverse --remove tcp:48416',recorded)
            self.assertIn('adb pull /sdcard/Android/data/io.github.liveyum.iotools/files/aot-evidence/. android-evidence/aot-device/',recorded)
            self.assertEqual(apk.read_bytes(),b'normal-entry-immutable-signed-apk-fixture')
            for suite,driver,count in [('main',drive,6),('opcua',opcua,2)]:
                outcome=(root/f'android-evidence/flutter-{suite}-outcome.txt').read_text()
                self.assertIn(f'driver_exit={driver}\n',outcome)
                self.assertIn(f'expected_app_passes_including_teardown={count}\n',outcome)
                self.assertIn('app_outcome=',outcome)
                recorded += f'\n{suite} outcome:\n{outcome}'
            return result.returncode, recorded

    def test_all_pass_reinstalls_exact_saved_apk(self):
        for renderer in (0,13):
            with self.subTest(renderer_exit=renderer):
                self.assertEqual(self.run_case(renderer=renderer)[0],0)
    def test_pre_action_metadata_does_not_require_uninstalled_emulator(self):
        workflow=WORKFLOW.read_text()
        before,marker,remaining=workflow.partition('      - name: Detect already granted emulator acceleration\n        run: |\n')
        self.assertTrue(marker)
        step,marker,after=remaining.partition('      - name: Real emulator UI, protocol, editing and lifecycle tests\n')
        self.assertTrue(marker)
        self.assertIn('uses: reactivecircus/android-emulator-runner@v2',after)
        with tempfile.TemporaryDirectory(prefix='iotools-pre-emulator-') as directory:
            root=Path(directory)
            env=os.environ | {'ANDROID_HOME':str(root/'sdk-not-installed'), 'GITHUB_ENV':str(root/'github-env')}
            result=subprocess.run(['bash','-e','-c',textwrap.dedent(step)],env=env,cwd=root,capture_output=True,timeout=5)
            self.assertEqual(result.returncode,0,result.stderr.decode())
            evidence=(root/'android-evidence/emulator-graphics.txt').read_text()
            self.assertIn('baseline_emulator=37.2.12.0 (build_id 16428233)\n',evidence)
            self.assertIn('requested_gpu=software\n',evidence)
            self.assertNotIn('emulator -version',step)
            self.assertNotIn('/emulator/emulator',step)
            self.assertIn('ANDROID_EMULATOR_ACCEL=',(root/'github-env').read_text())
    def test_drive_failure_still_runs_aot_and_remains_failed(self):
        code,trace=self.run_case(drive=41)
        self.assertEqual(code,41)
        self.assertIn('verify scripts/flutter/verify-aot-device.py',trace)
    def test_failed_process_stop_never_runs_unisolated_suite(self):
        self.assertEqual(self.run_case(stop=46)[0],46)
    def test_opcua_failure_remains_failed_and_main_still_ran(self):
        code,trace=self.run_case(opcua=45)
        self.assertEqual(code,45)
        self.assertIn('verify scripts/flutter/verify-aot-device.py',trace)
    def test_first_suite_failure_is_not_replaced_by_opcua_failure(self):
        self.assertEqual(self.run_case(drive=41,opcua=45)[0],41)
    def test_plugin_failure_remains_failed(self):
        self.assertEqual(self.run_case(plugin=42)[0],42)
    def test_instrumentation_failure_is_not_success(self):
        self.assertEqual(self.run_case(instrument=43)[0],43)
    def test_aot_verification_failure_is_not_success(self):
        self.assertEqual(self.run_case(verification=44)[0],44)
    def test_cleanup_cannot_replace_first_failure(self):
        self.assertEqual(self.run_case(drive=41,instrument=43,cleanup=47)[0],41)
    def test_9bf_zero_driver_exit_cannot_hide_app_timeout(self):
        log='''I/flutter ( 4045): 08:00 +0: Flutter OPC whole app [E]
I/flutter ( 4045):   TimeoutException after 0:08:00.000000: Test timed out after 8 minutes.
I/flutter ( 4045): 08:03 +0 -1: (tearDownAll)
All tests passed.
I/flutter ( 4045): 08:06 +1 -1: Some tests failed.'''
        code,trace=self.run_case(opcua_log=log)
        self.assertEqual(code,1)
        self.assertIn('driver_exit=0\napp_outcome=failed',trace)
        self.assertIn('verify scripts/flutter/verify-aot-device.py',trace)
    def test_timeout_without_final_app_summary_is_failed(self):
        log='I/flutter ( 4045):   TimeoutException after 0:08:00.000000: Test timed out after 8 minutes.\nAll tests passed.'
        self.assertEqual(self.run_case(opcua_log=log)[0],1)
    def test_app_failure_overrides_even_a_conflicting_app_pass(self):
        log='I/flutter ( 4045): 08:06 +1 -1: Some tests failed.\nI/flutter ( 4045): 08:07 +2: All tests passed!'
        self.assertEqual(self.run_case(opcua_log=log)[0],1)
    def test_host_only_success_cannot_replace_app_completion(self):
        self.assertEqual(self.run_case(drive_log='All tests passed.')[0],1)
    def test_wrong_app_count_cannot_pass(self):
        self.assertEqual(self.run_case(opcua_log='I/flutter ( 456): 07:20 +1: All tests passed!')[0],1)
    def test_expected_inner_error_text_does_not_fail_passing_suite(self):
        log='''I/flutter ( 456): Expected inner error: TimeoutException after 0:08:00.000000: Test timed out after 8 minutes.
I/flutter ( 456): 00:02 +0: validation renders Some tests failed. as literal text
I/flutter ( 456): 07:20 +2: All tests passed!'''
        self.assertEqual(self.run_case(opcua_log=log)[0],0)
    def test_original_nonzero_driver_exit_wins_over_app_failure(self):
        self.assertEqual(self.run_case(opcua=45,opcua_log='I/flutter ( 456): 08:06 +1 -1: Some tests failed.')[0],45)

if __name__ == '__main__':
    unittest.main()
