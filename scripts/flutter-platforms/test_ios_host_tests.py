"""Strict split-XCTest contracts; fixtures do not execute Apple tools."""

import copy
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ios_host_tests as host

ID = "48b2da2e-af18-4e36-bb25-b25969a4ebcf"


def target():
    # Apple's xctestrun(5) documented application-hosted product placeholders.
    return {"BlueprintName": "RunnerTests", "ProductModuleName": "RunnerTests",
            "TestHostPath": "__TESTROOT__/Debug-iphonesimulator/Runner.app",
            "TestBundlePath": "__TESTHOST__/PlugIns/RunnerTests.xctest",
            "TestHostBundleIdentifier": "io.github.liveyum.iotools"}


def cases():
    return {"summaries": [{"testableSummaries": [{"targetName": "RunnerTests", "tests": [{
        "identifier": "RunnerTests", "subtests": [
            {"identifier": "RunnerTests/" + name + "()", "testStatus": "Success"}
            for name in sorted(host.EXPECTED_METHODS)]}]}]}]}


class DescriptorTests(unittest.TestCase):
    def test_documented_versions_keep_original_target(self):
        row = target()
        self.assertIs(host.descriptor_target({"RunnerTests": row}), row)
        self.assertIs(host.descriptor_target({"__xctestrun_metadata__": {"FormatVersion": 1}, "RunnerTests": row}), row)
        data = {"__xctestrun_metadata__": {"FormatVersion": 2},
                "TestConfigurations": [{"IsEnabled": True, "TestTargets": [row]}]}
        self.assertIs(host.descriptor_target(data), row)

    def test_missing_extra_disabled_and_wrong_target_descriptors_fail(self):
        variants = [{}, {"RunnerTests": target(), "OtherTests": target()},
                    {"__xctestrun_metadata__": {"FormatVersion": 99}},
                    {"RunnerTests": dict(target(), IsEnabled=False)},
                    {"RunnerTests": dict(target(), BlueprintName="OtherTests")},
                    {"RunnerTests": dict(target(), IsAppHostedTestBundle=False)},
                    {"RunnerTests": dict(target(), IsUITestBundle=True)},
                    {"RunnerTests": dict(target(), UseDestinationArtifacts=True)},
                    {"RunnerTests": dict(target(), SkipTestIdentifiers=["RunnerTests/testExportLimitsAndUTF8"])},
                    {"RunnerTests": dict(target(), OnlyTestIdentifiers=["RunnerTests/testExportLimitsAndUTF8"])}]
        for configuration in ({"IsEnabled": False, "TestTargets": [target()]},
                              {"TestTargets": []}, {"TestTargets": [target(), target()]},
                              {"TestTargets": [dict(target(), BlueprintName="OtherTests")]}):
            variants.append({"__xctestrun_metadata__": {"FormatVersion": 2}, "TestConfigurations": [configuration]})
        for value in variants:
            with self.subTest(value=value), self.assertRaises(RuntimeError):
                host.descriptor_target(value)

    def make_products(self, directory):
        products = Path(directory) / "Build/Products"
        app = products / "Debug-iphonesimulator/Runner.app"
        tests = app / "PlugIns/RunnerTests.xctest"
        tests.mkdir(parents=True)
        for bundle, identifier, executable in ((app, "io.github.liveyum.iotools", "Runner"),
                                              (tests, "io.github.liveyum.iotools.RunnerTests", "RunnerTests")):
            (bundle / "Info.plist").write_bytes(plistlib.dumps({"CFBundleIdentifier": identifier, "CFBundleExecutable": executable}))
            (bundle / executable).write_bytes(b"fixture executable " + executable.encode())
        descriptor = products / "Runner_iphonesimulator26.2-arm64.xctestrun"
        descriptor.write_bytes(plistlib.dumps({"RunnerTests": target()}))
        return products, app, tests, descriptor

    def test_actual_build_location_resolves_paths_and_bytes_are_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            products, app, tests, descriptor = self.make_products(directory)
            original = descriptor.read_bytes()
            found = host.descriptor_products(descriptor, products)
            self.assertEqual(found["host"]["bundle"], str(app.resolve()))
            self.assertEqual(found["tests"]["bundle"], str(tests.resolve()))
            self.assertEqual(found["tests"]["sha256"], host.digest(tests / "RunnerTests"))
            self.assertEqual(descriptor.read_bytes(), original)
            copy_path = Path(directory) / "evidence/generated.xctestrun"
            copy_path.parent.mkdir()
            copy_path.write_bytes(original)
            with self.assertRaisesRegex(RuntimeError, "escapes|missing"):
                host.descriptor_products(copy_path, products)

    def test_symlinked_temporary_root_matches_canonical_products_without_allowing_escape(self):
        # macOS exposes /var as /private/var. Reproduce that alias explicitly on
        # every host instead of requiring this test itself to run on macOS.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            real = root / "private/var"
            real.mkdir(parents=True)
            alias = root / "var"
            alias.symlink_to(real, target_is_directory=True)
            products, app, tests, descriptor = self.make_products(alias)
            original = descriptor.read_bytes()
            found = host.descriptor_products(descriptor, products)
            self.assertNotEqual(app, app.resolve())
            self.assertEqual(found["host"]["bundle"], str(app.resolve()))
            self.assertEqual(found["tests"]["bundle"], str(tests.resolve()))
            self.assertEqual(descriptor.read_bytes(), original)
            foreign_products, foreign_app, _, _ = self.make_products(root / "foreign")
            escaped = products / "escape"
            escaped.symlink_to(foreign_products, target_is_directory=True)
            descriptor.write_bytes(plistlib.dumps({"RunnerTests": dict(
                target(), TestHostPath="__TESTROOT__/escape/Debug-iphonesimulator/Runner.app")}))
            with self.assertRaisesRegex(RuntimeError, "escapes"):
                host.descriptor_products(descriptor, products)
            self.assertTrue(foreign_app.is_dir())

    def test_wrong_host_identity_escape_missing_and_foreign_products_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            products, app, tests, descriptor = self.make_products(directory)
            for changes in ({"TestHostPath": str(Path(directory))},
                            {"TestHostPath": "__PLATFORMS__/xctest"},
                            {"TestBundlePath": "__TESTHOST__/PlugIns/OtherTests.xctest"},
                            {"TestHostBundleIdentifier": "wrong.host"}):
                descriptor.write_bytes(plistlib.dumps({"RunnerTests": dict(target(), **changes)}))
                with self.subTest(changes=changes), self.assertRaises(RuntimeError):
                    host.descriptor_products(descriptor, products)
            descriptor.write_bytes(plistlib.dumps({"RunnerTests": target()}))
            (tests / "Info.plist").write_bytes(plistlib.dumps({"CFBundleIdentifier": "wrong", "CFBundleExecutable": "RunnerTests"}))
            with self.assertRaisesRegex(RuntimeError, "identity"):
                host.descriptor_products(descriptor, products)


class ResultTests(unittest.TestCase):
    def test_exact_five_successes_are_required(self):
        self.assertEqual(host.verify_cases({"passedTests": 5, "failedTests": 0, "skippedTests": 0}, cases()),
                         sorted(host.EXPECTED_METHODS))

    def test_skipped_failed_extra_and_missing_counts_fail(self):
        for summary in ({"passedTests": 5, "failedTests": 0},
                        {"passedTests": 5, "failedTests": 0, "skippedTests": 1},
                        {"passedTests": 5, "failedTests": 1, "skippedTests": 0},
                        {"passedTests": 6, "failedTests": 0, "skippedTests": 0},
                        {"passedTests": 0, "failedTests": 0, "skippedTests": 0}):
            with self.subTest(summary=summary), self.assertRaises(RuntimeError):
                host.verify_cases(summary, cases())

    def test_summary_count_cannot_hide_wrong_duplicate_or_skipped_cases(self):
        summary = {"passedTests": 5, "failedTests": 0, "skippedTests": 0}
        for mutation in ("duplicate", "wrong", "skip", "target", "extra-plan", "missing"):
            tree = cases()
            target_result = tree["summaries"][0]["testableSummaries"][0]
            leaves = target_result["tests"][0]["subtests"]
            if mutation == "duplicate": leaves[0] = copy.deepcopy(leaves[1])
            if mutation == "wrong": leaves[0]["identifier"] = "OtherClass/testExportLimitsAndUTF8()"
            if mutation == "skip": leaves[0]["testStatus"] = "Skipped"
            if mutation == "target": target_result["targetName"] = "OtherTests"
            if mutation == "extra-plan": tree["summaries"].append(copy.deepcopy(tree["summaries"][0]))
            if mutation == "missing": leaves.pop()
            with self.subTest(mutation=mutation), self.assertRaises(RuntimeError):
                host.verify_cases(summary, tree)

    def test_xcresult_typed_json_is_decoded(self):
        data = {"_type": {"_name": "ActionResult"}, "status": {"_value": "succeeded"},
                "rows": {"_values": [{"identifier": {"_value": "RunnerTests/testX()"}}]}}
        self.assertEqual(host.decode_result(data), {"status": "succeeded", "rows": [{"identifier": "RunnerTests/testX()"}]})


class PhaseTests(unittest.TestCase):
    def test_frozen_profile_rejects_previous_toolchain_and_mismatched_evidence(self):
        for mismatch in ("developer", "selection-sdk", "version", "build", "sdk"):
            with self.subTest(mismatch=mismatch), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                revision = "a" * 40
                selection = {"source_sha": revision, "status": "selected",
                             "developer_dir": host.PINNED_DEVELOPER,
                             "simulator_sdk": "26.2", "simulator": {"udid": ID}}
                environment = {"IOTOOLS_SHA": revision, "DEVELOPER_DIR": host.PINNED_DEVELOPER}
                version, sdk = "Xcode 26.2\nBuild version 17C52", "26.2"
                if mismatch == "developer":
                    environment["DEVELOPER_DIR"] = "/Applications/Xcode_16.4.app/Contents/Developer"
                if mismatch == "selection-sdk": selection["simulator_sdk"] = "18.5"
                if mismatch == "version": version = "Xcode 16.4\nBuild version 16F6"
                if mismatch == "build": version = "Xcode 26.2\nBuild version 17C529"
                if mismatch == "sdk": sdk = "18.5"
                (root / "xcode-selection.json").write_text(json.dumps(selection))
                with patch.dict(os.environ, environment), \
                     patch.object(host.subprocess, "check_output", side_effect=[version, sdk]) as output, \
                     patch.object(host.subprocess, "run") as run:
                    with self.assertRaisesRegex(RuntimeError, "frozen installed|validated same-run|Frozen Xcode/SDK"):
                        host.run_host_tests(root, root, ID, "arm64", [], lambda *_: self.fail("must not launch"))
                    run.assert_not_called()
                    self.assertLessEqual(output.call_count, 2)

    def test_claimed_revision_cannot_replace_actual_checkout_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            revision = "a" * 40
            (root / "xcode-selection.json").write_text(json.dumps({
                "source_sha": revision, "status": "selected", "developer_dir": host.PINNED_DEVELOPER,
                "simulator_sdk": "26.2", "simulator": {"udid": ID}}))
            with patch.dict(os.environ, {"IOTOOLS_SHA": revision, "DEVELOPER_DIR": host.PINNED_DEVELOPER}), \
                 patch.object(host.subprocess, "check_output", side_effect=["Xcode 26.2\nBuild version 17C52", "26.2", "b" * 40]), \
                 patch.object(host.subprocess, "run") as run:
                with self.assertRaisesRegex(RuntimeError, "Checkout HEAD"):
                    host.run_host_tests(root, root, ID, "arm64", [], lambda *_: self.fail("must not launch"))
                run.assert_not_called()

    def test_two_phase_orchestration_binds_actual_host_and_never_retries(self):
        for failure in (None, "build-for-testing", "test-without-building", "wrong-result-device", "final-result-deadline"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                evidence = root / "evidence"
                evidence.mkdir()
                revision = "a" * 40
                (evidence / "xcode-selection.json").write_text(json.dumps({
                    "source_sha": revision, "status": "selected", "developer_dir": host.PINNED_DEVELOPER,
                    "simulator_sdk": "26.2", "simulator": {"udid": ID}}))
                clock = [0]
                phases = []
                launched = []
                budgets = []
                def output(command, **kwargs):
                    if command[-1] == "-version": return "Xcode 26.2\nBuild version 17C52"
                    if command[-1] == "--show-sdk-version": return "26.2"
                    if command[-2:] == ["rev-parse", "HEAD"]: return revision
                    if "ls-files" in command: return ""
                    if "lipo" in command: return "arm64"
                    if "vtool" in command: return "  platform IOSSIMULATOR\n"
                    if "nm" in command:
                        return " ".join("_IotoolsNative" + name for name in ("ABIVersion", "Open", "Command", "Lifecycle", "Free"))
                    if "summary" in command: return json.dumps({"passedTests": 5, "failedTests": 0, "skippedTests": 0})
                    if "--id" in command:
                        if failure == "final-result-deadline":
                            clock[0] = 1200
                        return json.dumps(cases())
                    if "xcresulttool" in command:
                        return json.dumps({"actions": [{"actionResult": {"status": "succeeded", "testsRef": {"id": "cases"}},
                            "runDestination": {"targetDeviceRecord": {"identifier": "wrong" if failure == "wrong-result-device" else ID}}}]})
                    self.fail(f"Unexpected command {command}")
                def execute(command, **kwargs):
                    if command[0] == "git": return subprocess.CompletedProcess(command, 0)
                    action = next(part for part in command if part in ("build-for-testing", "test-without-building"))
                    phases.append(action)
                    budgets.append(kwargs["timeout"])
                    if action == "build-for-testing":
                        DescriptorTests().make_products(root / "mobile/build/ios-host-tests")
                        clock[0] += 400
                    else:
                        self.assertEqual(len(launched), 1)
                        self.assertTrue(command[command.index("-xctestrun") + 1].startswith(str(root / "mobile/build/ios-host-tests")))
                        clock[0] += 300
                    return subprocess.CompletedProcess(command, 65 if failure == action else 0)
                def launch(app, deadline):
                    launched.append(app)
                    self.assertEqual(app, (root / "mobile/build/ios-host-tests/Build/Products/Debug-iphonesimulator/Runner.app").resolve())
                    self.assertEqual(deadline, 1200)
                    clock[0] += 100
                with patch.dict(os.environ, {"IOTOOLS_SHA": revision, "DEVELOPER_DIR": host.PINNED_DEVELOPER}), \
                     patch.object(host.time, "monotonic", side_effect=lambda: clock[0]), \
                     patch.object(host, "source_inventory", return_value={"RunnerTests.swift": "source-hash"}), \
                     patch.object(host.subprocess, "check_output", side_effect=output), \
                     patch.object(host.subprocess, "run", side_effect=execute):
                    if failure:
                        with self.assertRaises(RuntimeError):
                            host.run_host_tests(root, evidence, ID, "arm64", ["xcrun", "xcodebuild"], launch)
                    else:
                        result = host.run_host_tests(root, evidence, ID, "arm64", ["xcrun", "xcodebuild"], launch)
                        self.assertEqual(result["status"], "passed_all_five_host_tests")
                        self.assertEqual(result["passed_identifiers"], sorted(host.EXPECTED_METHODS))
                self.assertEqual(phases, ["build-for-testing"] if failure == "build-for-testing" else
                                 ["build-for-testing", "test-without-building"])
                self.assertEqual(budgets, [1200] if failure == "build-for-testing" else [1200, 700])
                record = json.loads((evidence / "host-test-phases.json").read_text())
                self.assertEqual(record["status"], "failed" if failure else "passed_all_five_host_tests")
                if failure == "final-result-deadline":
                    self.assertIn("budget expired", record["error"])
                    self.assertNotIn("passed_identifiers", record)
                if failure == "build-for-testing": self.assertEqual(launched, [])

    def test_generic_build_and_concrete_execution_use_original_descriptor(self):
        work = Path("/repo/mobile/build/ios-host-tests")
        command = host.build_command(["xcrun", "xcodebuild", "-scheme", "Runner", "ARCHS=arm64",
                                      "ONLY_ACTIVE_ARCH=NO", "BUILD_DIR=/old"], work, Path("/evidence/build.xcresult"))
        self.assertIn("build-for-testing", command)
        self.assertEqual(command[command.index("-destination") + 1], "generic/platform=iOS Simulator")
        self.assertNotIn("BUILD_DIR=/old", command)
        descriptor = work / "Build/Products/actual.xctestrun"
        command = host.test_command(descriptor, ID, Path("/evidence/test.xcresult"))
        self.assertEqual(command[command.index("-xctestrun") + 1], str(descriptor))
        self.assertEqual(command[command.index("-destination") + 1], "platform=iOS Simulator,id=" + ID)
        # Xcode 26.2 rejects -test-iterations 1. A single execution omits all
        # optional repetition controls and is still checked for five results.
        self.assertFalse(set(command) & {"test", "build", "-workspace", "-project",
                                        "-test-iterations", "-retry-tests-on-failure",
                                        "-run-tests-until-failure", "-test-repetition-relaunch-enabled"})
        self.assertIn("-only-testing:RunnerTests", command)

    def test_failed_phase_is_executed_once_and_keeps_remaining_budget(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(host.time, "monotonic", return_value=1100), \
                patch.object(host.subprocess, "run", return_value=subprocess.CompletedProcess([], 65)) as run:
            with self.assertRaisesRegex(RuntimeError, "phase failed"):
                host.execute_once(["xcrun", "xcodebuild", "build-for-testing"], Path(directory) / "build.log", 1200)
            run.assert_called_once()
            self.assertEqual(run.call_args.kwargs["timeout"], 100)

    def test_expired_shared_deadline_does_not_start_next_phase(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(host.time, "monotonic", return_value=1201), \
                patch.object(host.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "budget expired"):
                host.execute_once(["xcrun", "xcodebuild", "test-without-building"], Path(directory) / "test.log", 1200)
            run.assert_not_called()

    def test_every_normal_launch_operation_uses_the_remaining_gate_budget(self):
        spec = importlib.util.spec_from_file_location("verify_ios_budget", Path(__file__).with_name("verify-ios.py"))
        verifier = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(verifier)
        app = Path("/fresh/Runner.app")
        with tempfile.TemporaryDirectory() as directory:
            commands = []
            def output(*args, **kwargs):
                commands.append((args, kwargs))
                self.assertLessEqual(kwargs["timeout"], 7)
                if args[1:4] == ("simctl", "list", "devices"):
                    return json.dumps({"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-26-2": [{
                        "name": "iPhone SE (3rd generation)", "udid": ID, "isAvailable": True, "state": "Shutdown"}]}})
                if "launchctl" in args: return "123\t0\tio.github.liveyum.iotools"
                return "started"
            with patch.object(verifier, "run", side_effect=output), \
                 patch.object(verifier, "remaining", return_value=7), \
                 patch.object(verifier.time, "sleep") as sleep, \
                 patch.object(verifier.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as terminate:
                report = {}
                verifier.normal_launch_simulator(app, {"CFBundleIdentifier": "io.github.liveyum.iotools"},
                    Path(directory), report, lambda: None, "26.2", ID, 1200)
            sleep.assert_called_once_with(5)
            self.assertTrue(all(call.kwargs["timeout"] <= 7 for call in terminate.call_args_list))
            installed = [args for args, _ in commands if "install" in args]
            self.assertEqual(len(installed), 1)
            self.assertEqual(installed[0][-1], str(app))
            self.assertEqual(report["normal_launch"]["still_running_after_seconds"], 5)


if __name__ == "__main__":
    unittest.main()
