#!/usr/bin/env python3
"""Hermetic fake-ADB/fixture tests. Never call real device, Go or Flutter tools."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import aot_diagnostics as diagnostic

ROOT = Path(__file__).resolve().parents[2]
APP = b"immutable-fake-aot-apk"
TEST = b"immutable-fake-original-instrumentation"
APP_HASH = hashlib.sha256(APP).hexdigest()
TEST_HASH = hashlib.sha256(TEST).hexdigest()


def verification(**changes):
    report = {
        "status": "passed", "restored_private_config_and_preferences": True,
        "build_sha": diagnostic.SOURCE_SHA, "apk_sha256": APP_HASH, "debuggable": False,
        "protocols": {key: True for key in ("http", "mqtt", "kafka", "modbus", "opcua")},
        "startup_recovery_cancel_preserved_original": True,
        "startup_recovery_opened_valid_collection": True, "startup_recovery_damaged_file_unchanged": True,
        "http_post_cancelled_without_write": True, "http_post_confirmed_once": True,
    }
    return report | changes


class FakeProcess:
    def __init__(self, case, args, stdout, stderr):
        self.case = case
        self.instrument = args[0] == "adb"
        self.args = args
        self.code = None
        self.waits = []
        self.killed = False
        self.terminated = False
        case["processes"].append(self)
        if self.instrument:
            require = "-e", "build_sha", diagnostic.SOURCE_SHA
            assert " ".join(require) in " ".join(args)
            text = case.get("instrument_text", "OK (1 test)\n")
            stdout.write(text.encode())
            stdout.flush()
        elif not case.get("fixture_not_ready"):
            Path(args[2]).write_text("{}")

    def poll(self):
        return self.code

    def wait(self, timeout):
        self.waits.append(timeout)
        if self.code is not None:
            return self.code
        if self.instrument:
            if len(self.waits) == 1 or self.case.get("instrument_timeout"):
                raise subprocess.TimeoutExpired(self.args, timeout)
            self.code = self.case.get("instrument_exit", 0)
        elif self.terminated and self.case.get("fixture_hang_on_terminate"):
            raise subprocess.TimeoutExpired(self.args, timeout)
        else:
            self.code = 0
        return self.code

    def terminate(self):
        self.terminated = True
        if not self.case.get("fixture_hang_on_terminate"):
            self.code = 0

    def kill(self):
        self.killed = True
        self.code = -9


class FakeADB(diagnostic.Commands):
    case = {}

    def run(self, name, args, timeout=10, required=True, env=None):
        self.case.setdefault("calls", []).append((name, args, timeout, env))
        if name == "original-verifier.txt":
            # Exercise the repository's real Python acceptance verifier against
            # synthetic reports; this cannot launch Go, Flutter or any device.
            return super().run(name, args, timeout, required, env)
        output, code = b"OK\n", 0
        if name == "devices.txt":
            output = self.case.get("devices", "List of devices attached\nemulator-5554 device\n").encode()
        elif name == "ro.boot.qemu.txt":
            output = b"1\n"
        elif name == "ro.build.version.sdk.txt":
            output = self.case.get("sdk", "29\n").encode()
        elif name == "ro.product.cpu.abi.txt":
            output = b"x86_64\n"
        elif name == "ro.build.fingerprint.txt":
            output = (diagnostic.FINGERPRINT + "\n").encode()
        elif name == "preinstalled.txt":
            output = self.case.get("preinstalled", "").encode()
        elif name == "preexisting-pid.txt":
            output, code = self.case.get("preexisting_pid", "").encode(), 1
        elif name == "installed-path.txt":
            output = b"package:/data/app/synthetic/base.apk\n"
        elif name == "installed-sha256.txt":
            output = (self.case.get("installed_hash", APP_HASH) + "  /data/app/synthetic/base.apk\n").encode()
        elif name == "launch.txt":
            output = self.case.get("launch", "Status: ok\nActivity: io.github.liveyum.iotools/.MainActivity\n").encode()
        elif name.endswith("-pid.txt"):
            output = self.case.get("pid", "4444\n").encode()
        elif name.endswith("-screen.png"):
            output = b"\x89PNG\r\n\x1a\nsynthetic-screen"
        elif name.endswith("-window.txt"):
            output = self.case.get("window", "mCurrentFocus=Window{123 u0 io.github.liveyum.iotools/.MainActivity}").encode()
        elif name.endswith("-logcat.txt"):
            log = "10-03 01:17:22.100 1949 1979 I ActivityManager: Start proc 4444:io.github.liveyum.iotools/u0a120 for activity\n"
            if not self.case.get("no_profile"):
                log += "10-03 01:17:30.346 4444 4608 D ProfileInstaller: Installing profile for io.github.liveyum.iotools\n"
            if self.case.get("skip_profile"):
                log += "10-03 01:17:30.350 4444 4608 D ProfileInstaller: Skipping profile installation for io.github.liveyum.iotools\n"
            if self.case.get("crash"):
                log += "10-03 01:17:30.995 4444 4608 F libc: Fatal signal 11 (SIGSEGV), code 1, fault addr 0x30 in tid 4608, pid 4444 (liveyum.iotools)\n"
            if self.case.get("restart"):
                log += "10-03 01:17:35.100 1949 1979 I ActivityManager: Start proc 4555:io.github.liveyum.iotools/u0a120 for activity\n"
            if self.case.get("death"):
                log += "10-03 01:17:35.200 1949 1979 I ActivityManager: Process io.github.liveyum.iotools (pid 4444) has died: fore TOP\n"
            output = log.encode()
        elif name in ("pull-evidence.txt", "failed-pull-evidence.txt"):
            destination = Path(args[-1])
            destination.mkdir(exist_ok=True)
            diagnostic.write_json(destination / "aot-verification.json", verification(**self.case.get("report", {})))
        elif name in ("tombstone-list.txt", "tombstones-pull.txt", "native-backtrace.txt"):
            output, code = b"Permission denied\n", 1
        if name in self.case.get("failures", {}):
            code = self.case["failures"][name]
        text, code = self.record(name, args, code, output, b"fake failure" if code else b"", timeout)
        diagnostic.require(not required or code == 0, f"{name} failed (exit {code})")
        return text, code


class DiagnosticTests(unittest.TestCase):
    def run_case(self, arm="cold-launch", **case):
        with tempfile.TemporaryDirectory(prefix="iotools-aot-diag-test-") as folder:
            root = Path(folder)
            inputs, output = root / "inputs", root / "evidence"
            inputs.mkdir()
            (inputs / "app.apk").write_bytes(APP)
            (inputs / "instrumentation.apk").write_bytes(TEST)
            specs = tuple(dict(spec, sha256=expected) for spec, expected in zip(diagnostic.ARTIFACTS, (APP_HASH, TEST_HASH)))
            identity = {"status": "verified", "repository": diagnostic.REPOSITORY, "source_run": diagnostic.SOURCE_RUN,
                        "packaged_source_sha": diagnostic.SOURCE_SHA, "artifacts": list(specs)}
            identity.update(case.pop("identity", {}))
            diagnostic.write_json(inputs / "identity.json", identity)
            if case.pop("wrong_app_hash", False):
                (inputs / "app.apk").write_bytes(b"wrong-app")
            if case.pop("wrong_test_hash", False):
                (inputs / "instrumentation.apk").write_bytes(b"wrong-test")
            case["processes"] = []
            sleeps = []
            FakeADB.case = case
            with patch.object(diagnostic, "APK_SHA", APP_HASH), patch.object(diagnostic, "TEST_SHA", TEST_HASH), \
                    patch.object(diagnostic, "ARTIFACTS", specs), \
                    patch.object(diagnostic.subprocess, "Popen", side_effect=lambda args, **kw: FakeProcess(case, args, **kw)):
                # Popen is patched only for the runner fixture/instrumentation.
                # subprocess.run in the verifier needs its real implementation.
                real_run = diagnostic.Commands.run
                def verifier_run(commands, name, args, timeout=10, required=True, env=None):
                    if name == "original-verifier.txt":
                        with patch.object(diagnostic.subprocess, "Popen", REAL_POPEN):
                            return real_run(commands, name, args, timeout, required, env)
                    return real_run(commands, name, args, timeout, required, env)
                with patch.object(diagnostic.Commands, "run", verifier_run):
                    code = diagnostic.diagnose(arm, inputs, output, root / "fixture", ROOT,
                                               commands_class=FakeADB, sleep=sleeps.append)
                    with self.assertRaises(FileExistsError):
                        diagnostic.diagnose(arm, inputs, output, root / "fixture", ROOT,
                                             commands_class=FakeADB, sleep=sleeps.append)
            report = json.loads((output / "result.json").read_text())
            records = json.loads((output / "commands.json").read_text()) if (output / "commands.json").exists() else []
            self.assertFalse(report["release_gate_passed"])
            self.assertEqual(report["packaged_source_sha"], diagnostic.SOURCE_SHA)
            self.assertLessEqual(sum("am" in x["command"] and "start" in x["command"] for x in records), 1)
            self.assertLessEqual(sum("instrument" in x["command"] for x in records), 1)
            self.assertFalse(any(x in ("root", "su", "setenforce", "uninstall") for r in records for x in r["command"]))
            self.assertFalse(any("install" in r["command"] and "-r" in r["command"] for r in records))
            return code, report, records, case, sleeps

    def test_cold_once_survives_but_never_claims_acceptance(self):
        code, report, records, _, sleeps = self.run_case()
        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "cold_launch_survived_not_acceptance")
        self.assertFalse(report["acceptance_passed"])
        self.assertEqual(report["attempts"], 1)
        self.assertEqual(sleeps, [30])
        self.assertEqual(sum("install" in x["command"] for x in records), 1)
        self.assertEqual(sum("force-stop" in x["command"] for x in records), 2)

    def test_standalone_original_test_and_verifier_pass(self):
        code, report, records, case, _ = self.run_case("standalone")
        self.assertEqual(code, 0, report)
        self.assertTrue(report["acceptance_passed"])
        self.assertEqual(report["attempts"], 1)
        self.assertEqual(sum("install" in x["command"] for x in records), 2)
        self.assertEqual(sum("--remove" in x["command"] for x in records), 7)
        instrument = next(x for x in records if "instrument" in x["command"])
        self.assertEqual(instrument["timeout_seconds"], 600)
        self.assertIn(diagnostic.SOURCE_SHA, instrument["command"])
        self.assertTrue(case["processes"][0].terminated)

    def test_wrong_hashes_stop_before_adb(self):
        for flag in ("wrong_app_hash", "wrong_test_hash"):
            with self.subTest(flag=flag):
                code, report, records, _, _ = self.run_case(**{flag: True})
                self.assertEqual(code, 1)
                self.assertEqual(records, [])
                self.assertEqual(report["attempts"], 0)

    def test_wrong_source_identity_stops_before_adb(self):
        code, _, records, _, _ = self.run_case(identity={"packaged_source_sha": "0" * 40})
        self.assertEqual((code, records), (1, []))

    def test_refuses_warm_state_real_devices_wrong_api_without_cleanup_mutations(self):
        for case in ({"preinstalled": "package:" + diagnostic.PACKAGE + "\n"},
                     {"preinstalled": "package:" + diagnostic.PACKAGE + ".test\n"},
                     {"preexisting_pid": "3333"}, {"sdk": "35\n"},
                     {"devices": "List of devices attached\nphysical123 device\n"}):
            with self.subTest(case=case):
                code, report, records, _, _ = self.run_case(**case)
                self.assertEqual(code, 1)
                self.assertEqual(report["attempts"], 0)
                self.assertFalse(any("install" in x["command"] or "force-stop" in x["command"] for x in records))

    def test_no_profile_is_nonzero_inconclusive(self):
        for arm in ("cold-launch", "standalone"):
            with self.subTest(arm=arm):
                code, report, _, _, _ = self.run_case(arm, no_profile=True)
                self.assertEqual(code, 2)
                self.assertEqual(report["status"], "inconclusive")

    def test_profile_skip_is_inconclusive_even_with_install_log(self):
        code, report, _, _, _ = self.run_case(skip_profile=True)
        self.assertEqual(code, 2)
        self.assertIn("inconclusive", report["profile_result"])

    def test_crash_or_restart_never_green(self):
        for cause in ("crash", "restart", "death"):
            with self.subTest(cause=cause):
                code, report, _, _, _ = self.run_case(**{cause: True})
                self.assertEqual(code, 1)
                self.assertEqual(report["attempts"], 1)

    def test_normal_instrumentation_completion_death_is_not_native_crash(self):
        code, report, _, _, _ = self.run_case("standalone", death=True)
        self.assertEqual(code, 0, report)
        self.assertFalse(report["crash_detected"])

    def test_fatal_crash_overrides_apparent_instrumentation_pass(self):
        code, report, _, _, _ = self.run_case("standalone", crash=True)
        self.assertEqual(code, 1)
        self.assertTrue(report["original_verifier_passed"])
        self.assertFalse(report["acceptance_passed"])

    def test_incomplete_log_collection_fails_closed(self):
        for arm in ("cold-launch", "standalone"):
            code, report, _, _, _ = self.run_case(arm, failures={"finish-logcat.txt": 124})
            self.assertEqual(code, 2)
            self.assertFalse(report["acceptance_passed"])

    def test_partial_install_timeout_is_not_retried_and_is_cleaned(self):
        code, report, records, _, _ = self.run_case(failures={"install-app.txt": 124})
        self.assertEqual(code, 1)
        self.assertEqual(report["attempts"], 0)
        self.assertEqual(sum("install" in x["command"] for x in records), 1)
        self.assertEqual(sum("force-stop" in x["command"] for x in records), 2)

    def test_installed_hash_mismatch_stops_before_launch_and_cleans(self):
        code, report, records, _, _ = self.run_case(installed_hash="0" * 64)
        self.assertEqual(code, 1)
        self.assertEqual(report["attempts"], 0)
        self.assertEqual(sum("force-stop" in x["command"] for x in records), 2)

    def test_launch_failure_and_timeout_are_not_retried(self):
        for case in ({"launch": "Error: not started"}, {"failures": {"launch.txt": 124}}):
            with self.subTest(case=case):
                code, report, records, _, _ = self.run_case(**case)
                self.assertEqual(code, 1)
                self.assertEqual(report["attempts"], 1)
                self.assertEqual(sum("start" in x["command"] for x in records), 1)

    def test_instrument_timeout_is_nonzero_killed_and_cleaned(self):
        code, report, records, case, _ = self.run_case("standalone", instrument_timeout=True)
        self.assertEqual(code, 1)
        self.assertIn("124", report["error"])
        self.assertTrue(case["processes"][1].killed)
        self.assertEqual(sum("--remove" in x["command"] for x in records), 7)

    def test_instrument_crash_and_missing_ok_and_nonzero_never_pass(self):
        for case in ({"instrument_text": "INSTRUMENTATION_RESULT: shortMsg=Process crashed.\n"},
                     {"instrument_text": "INSTRUMENTATION_CODE: 0\n"}, {"instrument_exit": 2}):
            with self.subTest(case=case):
                code, report, records, _, _ = self.run_case("standalone", **case)
                self.assertEqual(code, 1)
                self.assertFalse(report["acceptance_passed"])
                self.assertEqual(sum("instrument" in x["command"] for x in records), 1)

    def test_original_report_binding_protocol_and_cleanup_failures(self):
        for change in ({"build_sha": "0" * 40}, {"apk_sha256": "0" * 64}, {"status": "running"},
                       {"protocols": {}}, {"restored_private_config_and_preferences": False}):
            with self.subTest(change=change):
                code, report, _, _, _ = self.run_case("standalone", report=change)
                self.assertEqual(code, 1)
                self.assertFalse(report["acceptance_passed"])

    def test_cleanup_failure_never_turns_green_and_primary_error_survives(self):
        failure = {"cleanup-" + diagnostic.PACKAGE + ".txt": 124, "finish-screen.png": 1}
        code, report, _, _, _ = self.run_case("standalone", instrument_timeout=True, failures=failure)
        self.assertEqual(code, 1)
        self.assertIn("Instrumentation failed (exit 124)", report["error"])
        self.assertTrue(report["cleanup_errors"])

    def test_missing_focused_activity_and_dead_pid_fail(self):
        for case in ({"window": "mCurrentFocus=null"}, {"pid": ""}):
            self.assertEqual(self.run_case(**case)[0], 1)

    def test_fixture_readiness_bound_and_cleanup(self):
        code, report, records, case, sleeps = self.run_case("standalone", fixture_not_ready=True)
        self.assertEqual(code, 1)
        self.assertEqual(report["attempts"], 0)
        self.assertEqual(sleeps, [0.2] * 50)
        self.assertTrue(case["processes"][0].terminated)
        self.assertFalse(any("instrument" in x["command"] for x in records))

    def test_fixture_termination_timeout_forces_kill_and_failure(self):
        code, report, _, case, _ = self.run_case("standalone", fixture_hang_on_terminate=True)
        self.assertEqual(code, 1)
        self.assertTrue(case["processes"][0].killed)
        self.assertFalse(report["cleanup_passed"])

    def test_commands_timeout_is_recorded_without_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            commands = diagnostic.Commands(Path(directory))
            with patch.object(diagnostic.subprocess, "run", side_effect=subprocess.TimeoutExpired(["fake"], 4)) as run:
                with self.assertRaises(diagnostic.DiagnosticError):
                    commands.run("timeout.txt", ["fake"], timeout=4)
                self.assertEqual(run.call_count, 1)
                self.assertEqual(commands.records[0]["exit"], 124)


class ArtifactTests(unittest.TestCase):
    def test_metadata_requires_fixed_id_name_run_source_expiry_digest(self):
        spec = diagnostic.ARTIFACTS[0]
        metadata = {"id": spec["id"], "name": spec["name"], "expired": False,
                    "workflow_run": {"id": diagnostic.SOURCE_RUN, "head_sha": diagnostic.SOURCE_SHA},
                    "digest": "sha256:" + spec["archive_sha256"]}
        diagnostic.check_metadata(metadata, spec)
        for changes in ({"id": 1}, {"name": "other"}, {"expired": True}, {"digest": "sha256:wrong"},
                        {"workflow_run": {"id": 1, "head_sha": diagnostic.SOURCE_SHA}},
                        {"workflow_run": {"id": diagnostic.SOURCE_RUN, "head_sha": "0" * 40}}):
            with self.subTest(changes=changes), self.assertRaises(diagnostic.DiagnosticError):
                diagnostic.check_metadata(metadata | changes, spec)

    def test_archive_and_inner_hashes_fail_closed_and_no_arbitrary_extraction(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / "archive.zip"
            with zipfile.ZipFile(archive, "w") as z:
                z.writestr("expected.apk", APP)
                z.writestr("../escape.txt", "untrusted")
            spec = {"member": "expected.apk", "output": "app.apk", "sha256": APP_HASH,
                    "archive_sha256": diagnostic.digest(archive)}
            for changes in ({"archive_sha256": "0" * 64}, {"sha256": "0" * 64}, {"member": "absent.apk"}):
                with self.subTest(changes=changes), self.assertRaises(diagnostic.DiagnosticError):
                    diagnostic.extract_verified(archive, spec | changes, root)
                self.assertFalse((root / "app.apk").exists())
            diagnostic.extract_verified(archive, spec, root)
            self.assertEqual((root / "app.apk").read_bytes(), APP)
            self.assertFalse((root.parent / "escape.txt").exists())


class WorkflowTests(unittest.TestCase):
    def test_two_fresh_creation_only_jobs_no_rebuild_no_privilege_or_auto_retry(self):
        workflow = (ROOT / ".github/workflows/android-aot-diagnostics.yml").read_text()
        self.assertNotIn("workflow_dispatch:", workflow)
        self.assertNotIn("pull_request:", workflow)
        self.assertIn("branches: ['diag/aot-1465-once']", workflow)
        self.assertIn("github.event.created == true", workflow)
        self.assertIn("github.run_attempt == 1", workflow)
        self.assertIn("github.event.before == '" + '0' * 40 + "'", workflow)
        self.assertIn("arm: [cold-launch, standalone]", workflow)
        self.assertIn("fail-fast: false", workflow)
        self.assertIn("force-avd-creation: true", workflow)
        self.assertIn("-no-snapshot -wipe-data", workflow)
        self.assertIn("emulator-build: 16428233", workflow)
        self.assertIn("api-level: 29", workflow)
        self.assertIn("-accel off", workflow)
        self.assertIn("disable-linux-hw-accel: true", workflow)
        self.assertNotIn("sudo", workflow)
        self.assertNotIn("actions/cache", workflow)
        self.assertNotIn("flutter build", workflow)
        self.assertNotIn("assemble", workflow)
        self.assertNotIn("continue-on-error", workflow)
        self.assertEqual(workflow.count("aot_diagnostics.py run --arm"), 1)
        self.assertIn("IOTOOLS_SHA: " + diagnostic.SOURCE_SHA, workflow)
        self.assertIn("if: always()", workflow)

    def test_only_first_reviewed_branch_creation_is_admitted(self):
        env = {'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': diagnostic.DIAGNOSTIC_REF,
               'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_SHA': 'a' * 40, 'DIAGNOSTIC_WORKFLOW_SHA': 'a' * 40}
        event = {'created': True, 'deleted': False, 'before': '0' * 40,
                 'after': 'a' * 40, 'ref': diagnostic.DIAGNOSTIC_REF}
        diagnostic.check_launch_context(env, event)
        for change in ({'GITHUB_EVENT_NAME': 'workflow_dispatch'}, {'GITHUB_EVENT_NAME': 'pull_request'},
                       {'GITHUB_REF': 'refs/heads/main'}, {'GITHUB_RUN_ATTEMPT': '2'},
                       {'GITHUB_SHA': 'b' * 40}, {'DIAGNOSTIC_WORKFLOW_SHA': 'b' * 40}):
            with self.subTest(env=change), self.assertRaises(diagnostic.DiagnosticError):
                diagnostic.check_launch_context(env | change, event)
        for change in ({'created': False}, {'deleted': True}, {'before': 'b' * 40},
                       {'after': 'b' * 40}, {'ref': 'refs/heads/main'}):
            with self.subTest(event=change), self.assertRaises(diagnostic.DiagnosticError):
                diagnostic.check_launch_context(env, event | change)


REAL_POPEN = subprocess.Popen

if __name__ == "__main__":
    unittest.main()
