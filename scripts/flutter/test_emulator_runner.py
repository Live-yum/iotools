#!/usr/bin/env python3
"""Hermetic runner exit/installation checks; fake processes, no Android or network."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

RUNNER = Path(__file__).with_name('run-emulator-tests.sh')

class RunnerTests(unittest.TestCase):
    def run_case(self, *, drive=0, opcua=0, stop=0, plugin=0, instrument=0, verification=0, cleanup=0):
        with tempfile.TemporaryDirectory(prefix='iotools-runner-') as directory:
            root = Path(directory)
            for name in ('scripts/flutter', 'mobile/android', 'android-dist', 'android-evidence/aot-test', 'bin'):
                (root/name).mkdir(parents=True, exist_ok=True)
            shutil.copyfile(RUNNER, root/'scripts/flutter/run-emulator-tests.sh')
            apk = root/'android-dist/iotools-flutter-universal-fixture-aot-test-signed.apk'
            apk.write_bytes(b'normal-entry-immutable-signed-apk-fixture')
            (root/'android-evidence/aot-test/instrumentation.apk').write_bytes(b'test-apk')
            def script(path, text):
                path.write_text('#!/usr/bin/env bash\nset -eu\n'+text+'\n')
                path.chmod(0o700)
            script(root/'mobile/android/gradlew', 'exit 0')
            script(root/'bin/flutter', 'printf "drive %s\\n" "$*" >> "$TRACE"; case "$*" in *opcua_workflow_test.dart*) exit "$OPCUA_RESULT";; *) exit "$DRIVE_RESULT";; esac')
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
            env = os.environ | {'PATH': str(root/'bin')+os.pathsep+os.environ['PATH'], 'RUNNER_TEMP':str(root), 'TRACE':str(trace), 'IOTOOLS_SHA':'a'*40, 'DRIVE_RESULT':str(drive), 'OPCUA_RESULT':str(opcua), 'STOP_RESULT':str(stop), 'PLUGIN_RESULT':str(plugin), 'INSTRUMENT_RESULT':str(instrument), 'VERIFY_RESULT':str(verification), 'CLEANUP_RESULT':str(cleanup)}
            result = subprocess.run(['bash',str(root/'scripts/flutter/run-emulator-tests.sh')], env=env, cwd=root, capture_output=True, timeout=15)
            recorded=trace.read_text()
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
            return result.returncode, recorded

    def test_all_pass_reinstalls_exact_saved_apk(self):
        self.assertEqual(self.run_case()[0],0)
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

if __name__ == '__main__':
    unittest.main()
