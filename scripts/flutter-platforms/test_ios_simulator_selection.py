#!/usr/bin/env python3
"""Lightweight regression tests; no Xcode, simulator or native build required."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("verify_ios", Path(__file__).with_name("verify-ios.py"))
verify_ios = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify_ios)


def device(name="iPhone SE (3rd generation)", *, booted=False, available=True):
    return {"name": name, "udid": name, "isAvailable": available,
            "state": "Booted" if booted else "Shutdown"}


class SimulatorSelectionTests(unittest.TestCase):
    def test_selected_xcode_sdk_wins_over_newer_booted_runtime(self):
        devices = {
            "com.apple.CoreSimulator.SimRuntime.iOS-26-2": [device(booted=True)],
            "com.apple.CoreSimulator.SimRuntime.iOS-18-6": [device()],
            "com.apple.CoreSimulator.SimRuntime.iOS-18-5": [device()],
        }
        runtime, _ = verify_ios.select_simulator(devices, "18.5")
        self.assertEqual(runtime, "com.apple.CoreSimulator.SimRuntime.iOS-18-5")

    def test_booted_phone_is_preferred_within_matching_release(self):
        booted = device("iPhone 16", booted=True)
        devices = {"com.apple.CoreSimulator.SimRuntime.iOS-18-5": [device(), booted]}
        self.assertIs(verify_ios.select_simulator(devices, "18.5")[1], booted)

    def test_patch_versions_keep_matching_major_minor(self):
        devices = {"com.apple.CoreSimulator.SimRuntime.iOS-26-0-1": [device()]}
        self.assertEqual(verify_ios.select_simulator(devices, "26.0.1")[0],
                         "com.apple.CoreSimulator.SimRuntime.iOS-26-0-1")

    def test_unavailable_phones_and_non_iphone_devices_are_ignored(self):
        chosen = device("iPhone 16")
        devices = {
            "com.apple.CoreSimulator.SimRuntime.iOS-18-5": [
                device("iPhone unavailable", available=False), device("iPad Pro", booted=True), chosen],
            "com.apple.CoreSimulator.SimRuntime.tvOS-18-5": [device(booted=True)],
        }
        self.assertIs(verify_ios.select_simulator(devices, "18.5")[1], chosen)

    def test_missing_matching_runtime_fails_instead_of_falling_back(self):
        devices = {"com.apple.CoreSimulator.SimRuntime.iOS-26-2": [device(booted=True)]}
        with self.assertRaisesRegex(RuntimeError, "matching selected SDK 18.5"):
            verify_ios.select_simulator(devices, "18.5")

    def test_invalid_sdk_version_fails_clearly(self):
        for version in ["", "unknown", "18.5beta", "18.5\n26.2"]:
            with self.subTest(version=version), self.assertRaisesRegex(RuntimeError, "Unexpected selected"):
                verify_ios.select_simulator({}, version)

    def test_pinned_eligible_device_wins_over_other_booted_phone(self):
        identifier = "22a3039c-6a45-47f4-82d4-80c58ca94379"
        selected = dict(device("iPhone 16"), udid=identifier.upper())
        devices = {"com.apple.CoreSimulator.SimRuntime.iOS-18-5": [device(booted=True), selected]}
        self.assertIs(verify_ios.select_simulator(devices, "18.5", identifier)[1], selected)

    def test_pinned_wrong_sdk_or_unavailable_device_never_falls_back(self):
        identifier = "22a3039c-6a45-47f4-82d4-80c58ca94379"
        for runtime, available in (("iOS-26-2", True), ("iOS-18-5", False)):
            with self.subTest(runtime=runtime, available=available):
                devices = {"com.apple.CoreSimulator.SimRuntime.iOS-18-5": [device(booted=True)]}
                devices.setdefault("com.apple.CoreSimulator.SimRuntime." + runtime, []).append(
                    dict(device(available=available), udid=identifier))
                with self.assertRaisesRegex(RuntimeError, "Pinned iPhone simulator"):
                    verify_ios.select_simulator(devices, "18.5", identifier)

    def test_malformed_pin_never_falls_back(self):
        devices = {"com.apple.CoreSimulator.SimRuntime.iOS-18-5": [device()]}
        for identifier in ("", "unknown", "22a3039c-6a45-47f4-82d4-80c58ca94379\n"):
            with self.subTest(identifier=identifier), self.assertRaisesRegex(RuntimeError, "Invalid pinned"):
                verify_ios.select_simulator(devices, "18.5", identifier)


class SimulatorTestCommandTests(unittest.TestCase):
    def test_simulator_sdk_architecture_and_all_host_test_gates_are_explicit(self):
        for architecture in ("arm64", "x86_64"):
            with self.subTest(architecture=architecture):
                result = Path("evidence/host-tests.xcresult")
                command = verify_ios.simulator_test_command("test-device-id", result, architecture)
                self.assertEqual(command[:2], ["xcrun", "xcodebuild"])
                self.assertEqual(command[command.index("-sdk") + 1], "iphonesimulator")
                self.assertEqual(command[command.index("-configuration") + 1], "Debug")
                self.assertEqual(command[command.index("-scheme") + 1], "Runner")
                self.assertEqual(command[command.index("-destination") + 1],
                                 "platform=iOS Simulator,id=test-device-id")
                destination_keys = {part.split("=", 1)[0] for part in command[command.index("-destination") + 1].split(",")}
                self.assertEqual(destination_keys, {"platform", "id"})
                self.assertIn(f"ARCHS={architecture}", command)
                self.assertIn("ONLY_ACTIVE_ARCH=NO", command)
                self.assertNotIn("ONLY_ACTIVE_ARCH=YES", command)
                self.assertIn("test", command)
                self.assertIn("-only-testing:RunnerTests", command)
                self.assertEqual(command[command.index("-parallel-testing-enabled") + 1], "NO")
                self.assertEqual(command[command.index("-resultBundlePath") + 1], str(result))
                for setting in ("CODE_SIGNING_ALLOWED=NO", "CODE_SIGNING_REQUIRED=NO", "CODE_SIGN_IDENTITY="):
                    self.assertIn(setting, command)

    def test_invalid_architecture_does_not_reach_xcodebuild(self):
        for architecture in ("", "arm64e", "arm64 x86_64"):
            with self.subTest(architecture=architecture), self.assertRaisesRegex(RuntimeError, "Unsupported simulator"):
                verify_ios.simulator_test_command("test-device-id", Path("result.xcresult"), architecture)

    def test_runner_tests_diagnostics_use_the_actual_test_target(self):
        command = verify_ios.simulator_xcode_settings("arm64", test_target=True)
        self.assertEqual(command[command.index("-target") + 1], "RunnerTests")
        self.assertEqual(command[command.index("-project") + 1], str(verify_ios.ROOT / "mobile/ios/Runner.xcodeproj"))
        self.assertNotIn("-workspace", command)
        self.assertNotIn("-scheme", command)
        self.assertIn("ARCHS=arm64", command)
        self.assertIn("CODE_SIGNING_ALLOWED=NO", command)
        self.assertEqual(command[command.index("-sdk") + 1], "iphonesimulator")

    def test_only_active_arch_alignment_is_the_only_test_command_change(self):
        baseline = verify_ios.simulator_test_command("device-id", Path("result.xcresult"), "arm64", only_active_arch="YES")
        aligned = verify_ios.simulator_test_command("device-id", Path("result.xcresult"), "arm64")
        self.assertEqual(aligned, ["ONLY_ACTIVE_ARCH=NO" if part == "ONLY_ACTIVE_ARCH=YES" else part for part in baseline])

    def test_split_preflight_is_information_only_and_has_no_old_architecture_experiment(self):
        calls = []
        def command(args, **kwargs):
            calls.append(args)
            self.assertNotIn("-resultBundlePath", args)
            self.assertTrue(set(args) & {"-showBuildSettings", "-showdestinations"})
            self.assertLessEqual(kwargs["timeout"], 40)
            self.assertNotIn("ONLY_ACTIVE_ARCH=YES", args)
            return subprocess.CompletedProcess(args, 0)
        with tempfile.TemporaryDirectory() as directory, patch.object(verify_ios.subprocess, "run", side_effect=command), \
                patch.object(verify_ios, "remaining", return_value=40):
            evidence = Path(directory)
            build = verify_ios.simulator_xcode_settings("arm64") + [
                "build-for-testing", "-destination", "generic/platform=iOS Simulator",
                "-resultBundlePath", str(evidence / "build.xcresult")]
            record = verify_ios.simulator_test_preflight("device-id", build, "arm64", evidence, 1200)
            self.assertEqual(len(calls), 3)
            self.assertEqual(sum("-target" in args for args in calls), 1)
            self.assertEqual(sum("build-for-testing" in args for args in calls), 2)
            self.assertEqual(record, json.loads((evidence / "xcode-test-command-comparison.json").read_text()))
            self.assertNotIn("host_tests", record)
            self.assertFalse((evidence / "build.xcresult").exists())

    def test_information_capture_rejects_bare_test_and_result_output(self):
        with tempfile.TemporaryDirectory() as directory:
            for command in (["xcrun", "xcodebuild", "test"],
                            ["xcrun", "xcodebuild", "test", "-showdestinations", "-resultBundlePath", "result.xcresult"]):
                with self.assertRaisesRegex(RuntimeError, "information-only"):
                    verify_ios.xcode_information(command, Path(directory), "diagnostic.log")


if __name__ == "__main__":
    unittest.main()
