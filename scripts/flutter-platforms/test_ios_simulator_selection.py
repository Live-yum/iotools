#!/usr/bin/env python3
"""Lightweight regression tests; no Xcode, simulator or native build required."""

import importlib.util
from pathlib import Path
import unittest

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
                                 f"platform=iOS Simulator,id=test-device-id,arch={architecture}")
                self.assertIn(f"ARCHS={architecture}", command)
                self.assertIn("ONLY_ACTIVE_ARCH=YES", command)
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


if __name__ == "__main__":
    unittest.main()
