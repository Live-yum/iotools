#!/usr/bin/env python3
"""Read-only Xcode discovery contract tests; no Apple tools required."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("select_ios_xcode", Path(__file__).with_name("select-ios-xcode.py"))
selector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(selector)

ID = "22a3039c-6a45-47f4-82d4-80c58ca94379"
DEVICES = {"com.apple.CoreSimulator.SimRuntime.iOS-18-5": [
    {"udid": ID.upper(), "isAvailable": True, "name": "iPhone SE (3rd generation)", "state": "Shutdown"}]}
HEADER = 'Available destinations for the "Runner" scheme:\n'
DESTINATION = f'{{ platform:iOS Simulator, arch:arm64, id:{ID.upper()}, OS:18.5, name:iPhone SE (3rd generation) }}\n'
PLACEHOLDERS = HEADER + '{ platform:iOS Simulator, id:dvtdevice-DVTiOSDeviceSimulatorPlaceholder-iphonesimulator:placeholder, name:Any iOS Simulator Device }\n'
XCDEVICES = [{"simulator": True, "operatingSystemVersion": "18.5 (22F77)", "available": True,
              "platform": "com.apple.platform.iphonesimulator", "identifier": ID.upper(),
              "architecture": "arm64", "name": "iPhone SE (3rd generation)", "ignored": False}]

# Exact xcdevice rows from run 36925567656, artifact 11194531259 (SHA 622a3b1).
# Current Xcode 16.4 returned ONLY this Mac despite showdestinations listing the
# iOS 18.5 ID above. Xcode 26.2 returned the available iOS 26.2 iPhone below.
# https://github.com/Live-yum/iotools/actions/runs/36925567656/artifacts/11194531259
ACTUAL_622_CURRENT_XCDEVICES = [{
    "ignored": False, "modelCode": "VirtualMac2,1", "simulator": False,
    "modelName": "Apple Virtual Machine 1", "operatingSystemVersion": "15.7.9 (24G830)",
    "identifier": "0f760c9e3e003d4e6ac04141a39bd09d4f2b12a8", "platform": "com.apple.platform.macosx",
    "architecture": "arm64e", "interface": "usb", "available": True, "name": "My Mac",
    "modelUTI": "com.apple.virtual-machine"}]
ACTUAL_622_26_XCDEVICE = {
    "simulator": True, "operatingSystemVersion": "26.2 (23C54)", "available": True,
    "platform": "com.apple.platform.iphonesimulator", "modelCode": "iPhone14,6",
    "identifier": "48B2DA2E-AF18-4E36-BB25-B25969A4EBCF", "architecture": "arm64",
    "modelUTI": "com.apple.iphone-se3-1", "modelName": "iPhone SE (3rd generation)",
    "name": "iPhone SE (3rd generation)", "ignored": False}
ACTUAL_622_26_DESTINATION = '{ platform:iOS Simulator, arch:arm64, id:48B2DA2E-AF18-4E36-BB25-B25969A4EBCF, OS:26.2, name:iPhone SE (3rd generation) }\n'
# Relevant unchanged fields from xcode-26.2-simctl.txt; paths/sizes are omitted.
ACTUAL_622_26_DEVICES = {"com.apple.CoreSimulator.SimRuntime.iOS-26-2": [{
    "udid": "48B2DA2E-AF18-4E36-BB25-B25969A4EBCF", "isAvailable": True,
    "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-SE-3rd-generation",
    "state": "Shutdown", "name": "iPhone SE (3rd generation)"}]}


class XcodeSelectionTests(unittest.TestCase):
    def test_05ed_placeholder_only_failure_is_never_accepted(self):
        self.assertEqual(selector.eligible_simulators(PLACEHOLDERS, DEVICES, XCDEVICES, "18.5", "arm64"), [])

    def test_concrete_matching_destination_is_accepted(self):
        selected = selector.eligible_simulators(HEADER + DESTINATION, DEVICES, XCDEVICES, "18.5", "arm64")
        self.assertEqual(selected[0]["udid"], ID)

    def test_ineligible_or_error_destination_is_rejected(self):
        for text in (HEADER + 'Ineligible destinations for the "Runner" scheme:\n' + DESTINATION,
                     HEADER + DESTINATION.replace(" }", ", error:Device is unavailable }")):
            self.assertEqual(selector.eligible_simulators(text, DEVICES, XCDEVICES, "18.5", "arm64"), [])

    def test_wrong_sdk_architecture_or_device_id_is_rejected(self):
        for text in (DESTINATION.replace("OS:18.5", "OS:26.2"),
                     DESTINATION.replace("arch:arm64", "arch:x86_64"),
                     DESTINATION.replace(ID.upper(), "00000000-0000-0000-0000-000000000001")):
            self.assertEqual(selector.eligible_simulators(HEADER + text, DEVICES, XCDEVICES, "18.5", "arm64"), [])

    def test_simctl_availability_is_required(self):
        unavailable = {key: [dict(value[0], isAvailable=False)] for key, value in DEVICES.items()}
        self.assertEqual(selector.eligible_simulators(HEADER + DESTINATION, unavailable, XCDEVICES, "18.5", "arm64"), [])

    def test_actual_622_current_xcdevice_zero_simulators_is_rejected(self):
        self.assertEqual(selector.eligible_simulators(
            HEADER + DESTINATION, DEVICES, ACTUAL_622_CURRENT_XCDEVICES, "18.5", "arm64"), [])

    def test_actual_622_xcode26_three_way_match_is_accepted(self):
        selected = selector.eligible_simulators(HEADER + ACTUAL_622_26_DESTINATION,
            ACTUAL_622_26_DEVICES, [ACTUAL_622_26_XCDEVICE], "26.2", "arm64")
        self.assertEqual(len(selected), 1)
        self.assertEqual(selected[0]["udid"], ACTUAL_622_26_XCDEVICE["identifier"].lower())
        self.assertEqual(selected[0]["xcdevice"], ACTUAL_622_26_XCDEVICE)

    def test_xcdevice_must_be_available_same_uuid_sdk_arch_and_simulator(self):
        variants = [[], ACTUAL_622_CURRENT_XCDEVICES]
        for changes in ({"simulator": False}, {"available": False}, {"ignored": True},
                        {"platform": "com.apple.platform.iphoneos"}, {"architecture": "x86_64"},
                        {"architecture": "arm64e"}, {"operatingSystemVersion": "26.2 (23C54)"},
                        {"operatingSystemVersion": "18.6"}, {"operatingSystemVersion": "unknown"},
                        {"identifier": "00000000-0000-0000-0000-000000000001"},
                        {"identifier": "not-a-uuid"}):
            variants.append([dict(XCDEVICES[0], **changes)])
        for field in ("simulator", "available", "platform", "architecture", "operatingSystemVersion", "identifier"):
            variants.append([{key: value for key, value in XCDEVICES[0].items() if key != field}])
        for rows in variants:
            with self.subTest(xcdevices=rows):
                self.assertEqual(selector.eligible_simulators(HEADER + DESTINATION, DEVICES, rows, "18.5", "arm64"), [])

    def test_xcdevice_case_and_sdk_patch_are_normalized_but_destination_arch_required(self):
        rows = [dict(XCDEVICES[0], identifier=ID, operatingSystemVersion="18.5.1 (22F99)")]
        self.assertEqual(len(selector.eligible_simulators(HEADER + DESTINATION, DEVICES, rows, "18.5", "arm64")), 1)
        self.assertEqual(selector.eligible_simulators(HEADER + DESTINATION.replace("arch:arm64, ", ""),
                         DEVICES, rows, "18.5", "arm64"), [])

    def test_malformed_xcdevice_json_shapes_fail_closed(self):
        for rows in ({}, [None], ["not a device"]):
            with self.assertRaisesRegex(ValueError, "device list"):
                selector.eligible_simulators(HEADER + DESTINATION, DEVICES, rows, "18.5", "arm64")

    def test_current_xcode_is_preferred_and_failed_probes_never_selected(self):
        valid = {"developer_dir": "current", "eligible_simulators": [{"udid": ID}]}
        other = dict(valid, developer_dir="26.2")
        failed = dict(valid, error="xcdevice query failed")
        self.assertIs(selector.choose_candidate([valid, other]), valid)
        self.assertIs(selector.choose_candidate([failed, other]), other)
        with self.assertRaisesRegex(RuntimeError, "Neither"):
            selector.choose_candidate([failed, {"eligible_simulators": []}])

    def test_probes_cannot_build_install_boot_or_change_global_xcode(self):
        commands = selector.probe_commands("arm64")
        self.assertEqual(set(commands), {"version", "sdk", "xcdevice", "simctl", "destinations"})
        for command in commands.values():
            self.assertEqual(command[0], "xcrun")
            self.assertFalse(set(command) & {"build", "test", "install", "boot", "sudo", "xcode-select", "-runFirstLaunch", "-downloadPlatform"})
        self.assertIn("-showdestinations", commands["destinations"])
        self.assertIn("CODE_SIGNING_ALLOWED=NO", commands["destinations"])

    def test_environment_is_job_scoped_and_keeps_existing_values(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "github-env"
            path.write_text("EXISTING=value\n")
            selected = {"developer_dir": str(selector.ALTERNATE), "eligible_simulators": [{"udid": ID}]}
            selector.write_environment(path, selected)
            self.assertEqual(path.read_text(), "EXISTING=value\n" + f"DEVELOPER_DIR={selector.ALTERNATE}\nIOTOOLS_IOS_SIMULATOR_UDID={ID}\n")
            with self.assertRaisesRegex(ValueError, "multiline"):
                selector.write_environment(path, dict(selected, developer_dir="bad\nINJECTED=true"))

    def test_probe_uses_process_local_environment_and_retains_stderr_destinations(self):
        def response(command, **kwargs):
            if command[-1] == "-version":
                return subprocess.CompletedProcess(command, 0, "Xcode 16.4\nBuild version 16F6\n", "")
            if command[-1] == "--show-sdk-version":
                return subprocess.CompletedProcess(command, 0, "18.5\n", "")
            if command[1] == "xcdevice":
                return subprocess.CompletedProcess(command, 0, json.dumps(XCDEVICES), "")
            if command[1] == "simctl":
                return subprocess.CompletedProcess(command, 0, json.dumps({"devices": DEVICES}), "")
            return subprocess.CompletedProcess(command, 0, "", HEADER + DESTINATION)

        original_environment = dict(os.environ)
        with tempfile.TemporaryDirectory() as directory, patch.object(selector.subprocess, "run", side_effect=response) as run:
            path = Path(directory)
            candidate = selector.probe(path, "current", path, "arm64")
            self.assertEqual(candidate["eligible_simulators"][0]["udid"], ID)
            self.assertEqual(run.call_count, 5)
            for call in run.call_args_list:
                self.assertEqual(call.kwargs["env"]["DEVELOPER_DIR"], str(path))
                self.assertLessEqual(call.kwargs["timeout"], 90)
            self.assertTrue((path / "current-destinations-stderr.txt").is_file())
            self.assertEqual(dict(os.environ), original_environment)

    def test_discovery_failure_is_recorded_without_selecting_or_mutating(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            with patch.object(selector.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "discovery failed")):
                candidate = selector.probe(path, "current", path, "arm64")
            self.assertIn("Read-only discovery failed", candidate["error"])
            self.assertEqual(candidate["eligible_simulators"], [])
            self.assertEqual(len(candidate["queries"]), 5)


if __name__ == "__main__":
    unittest.main()
