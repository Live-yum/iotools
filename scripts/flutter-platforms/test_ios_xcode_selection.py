#!/usr/bin/env python3
"""Read-only Xcode discovery contract tests; no Apple tools required."""

import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from datetime import datetime, timedelta, timezone
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


# Exact stdout from the failed a99f24277b9c200de206d48abd8adf0b5712b222 frozen
# selection, observed 2026-10-02T22:51:28.682062+00:00. The archive SHA-256 is
# 28dad3c8570c8606f2a34a2f4cb3c51645e72aaf8c04f09e10b4312c181d369f.
# simctl exited by its 30-second timeout with empty stdout; every other query
# succeeded, but xcdevice listed only the Mac and destinations only placeholders.
ACTUAL_A99_STARTUP_OUTPUTS = {'version': 'Xcode 26.2\nBuild version 17C52\n',
 'sdk': '26.2\n',
 'xcdevice': '[\n'
             '  {\n'
             '    "ignored" : false,\n'
             '    "modelCode" : "VirtualMac2,1",\n'
             '    "simulator" : false,\n'
             '    "modelName" : "Apple Virtual Machine 1",\n'
             '    "operatingSystemVersion" : "15.7.9 (24G830)",\n'
             '    "identifier" : "ef69742d45106fef6fd0eea68bb6a577435936e6",\n'
             '    "platform" : "com.apple.platform.macosx",\n'
             '    "architecture" : "arm64e",\n'
             '    "interface" : "usb",\n'
             '    "available" : true,\n'
             '    "name" : "My Mac",\n'
             '    "modelUTI" : "com.apple.virtual-machine"\n'
             '  }\n'
             ']\n',
 'destinations': 'Command line invocation:\n'
                 '    /Applications/Xcode_26.2.app/Contents/Developer/usr/bin/xcodebuild -workspace '
                 '/Users/runner/work/iotools/iotools/mobile/ios/Runner.xcworkspace -scheme Runner '
                 '-configuration Debug -sdk iphonesimulator ARCHS=arm64 ONLY_ACTIVE_ARCH=YES '
                 'CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= -showdestinations\n'
                 '\n'
                 'Build settings from command line:\n'
                 '    ARCHS = arm64\n'
                 '    CODE_SIGN_IDENTITY = \n'
                 '    CODE_SIGNING_ALLOWED = NO\n'
                 '    CODE_SIGNING_REQUIRED = NO\n'
                 '    ONLY_ACTIVE_ARCH = YES\n'
                 '    SDKROOT = iphonesimulator26.2\n'
                 '\n'
                 '\n'
                 '\n'
                 '\tAvailable destinations for the "Runner" scheme:\n'
                 '\t\t{ platform:macOS, arch:arm64, variant:Designed for [iPad,iPhone], '
                 'id:ef69742d45106fef6fd0eea68bb6a577435936e6, name:My Mac }\n'
                 '\t\t{ platform:iOS, id:dvtdevice-DVTiPhonePlaceholder-iphoneos:placeholder, name:Any iOS '
                 'Device }\n'
                 '\t\t{ platform:iOS Simulator, '
                 'id:dvtdevice-DVTiOSDeviceSimulatorPlaceholder-iphonesimulator:placeholder, name:Any iOS '
                 'Simulator Device }\n'}


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


FROZEN_ID = ACTUAL_622_26_XCDEVICE["identifier"].lower()
PREVIOUS_DEVELOPER_DIR = Path("/Applications/Xcode_16.4.app/Contents/Developer")


class FrozenXcodeSelectionTests(unittest.TestCase):
    def inventory(self, identifier=FROZEN_ID):
        return {
            "version": "Xcode 26.2\nBuild version 17C52\n",
            "sdk": "26.2\n",
            "xcdevice": json.dumps([dict(ACTUAL_622_26_XCDEVICE, identifier=identifier)]),
            "simctl": json.dumps({"devices": {
                runtime: [dict(device, udid=identifier) for device in rows]
                for runtime, rows in ACTUAL_622_26_DEVICES.items()}}),
            "destinations": HEADER + ACTUAL_622_26_DESTINATION.replace(FROZEN_ID.upper(), identifier),
        }

    def response(self, outputs):
        def run(command, **kwargs):
            if command[-1] == "-version":
                name = "version"
            elif command[-1] == "--show-sdk-version":
                name = "sdk"
            elif command[1] in ("xcdevice", "simctl"):
                name = command[1]
            else:
                name = "destinations"
            return subprocess.CompletedProcess(command, 0, outputs[name], "")
        return run

    def frozen_probe(self, outputs, architecture="arm64", developer_dir=None):
        developer_dir = developer_dir or selector.FROZEN_DEVELOPER_DIR
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(Path, "is_dir", return_value=True), \
                patch.object(selector.subprocess, "run", side_effect=self.response(outputs)) as run:
            candidate = selector.probe(developer_dir, "xcode-26.2", Path(directory), architecture,
                                       freeze_xcode_26_2=True)
            return candidate, run.call_args_list

    def test_exact_toolchain_model_and_observed_uuid_are_accepted(self):
        for identifier in (FROZEN_ID, "f107b59b-9864-4ae7-a12a-9104c143c89d"):
            with self.subTest(identifier=identifier):
                candidate, calls = self.frozen_probe(self.inventory(identifier))
                self.assertNotIn("error", candidate)
                self.assertEqual(candidate["eligible_simulators"][0]["udid"], identifier)
                self.assertEqual(len(calls), 5)
                for call in calls:
                    self.assertEqual(call.kwargs["env"]["DEVELOPER_DIR"], str(selector.FROZEN_DEVELOPER_DIR))

    def test_actual_622_xcode26_inventory_satisfies_frozen_profile(self):
        outputs = dict(self.inventory(), xcdevice=json.dumps([ACTUAL_622_26_XCDEVICE]),
                       simctl=json.dumps({"devices": ACTUAL_622_26_DEVICES}),
                       destinations=HEADER + ACTUAL_622_26_DESTINATION)
        candidate, calls = self.frozen_probe(outputs)
        self.assertNotIn("error", candidate)
        self.assertEqual(candidate["xcode_version"], "Xcode 26.2\nBuild version 17C52")
        self.assertEqual(candidate["simulator_sdk"], "26.2")
        self.assertEqual(candidate["eligible_simulators"][0]["udid"], FROZEN_ID)
        self.assertEqual(candidate["eligible_simulators"][0]["xcdevice"], ACTUAL_622_26_XCDEVICE)
        self.assertEqual(list(candidate["queries"]), ["version", "sdk", "xcdevice", "simctl", "destinations"])
        self.assertEqual([call.args[0] for call in calls], list(selector.probe_commands("arm64").values()))
        self.assertEqual([call.kwargs["timeout"] for call in calls], [30, 30, 30, 30, 90])
        self.assertEqual(calls[2].args[0], ["xcrun", "xcdevice", "list", "--timeout", "10"])

    def test_prior_164_inventory_cannot_satisfy_frozen_262_profile(self):
        outputs = dict(self.inventory(), xcdevice=json.dumps(XCDEVICES),
                       simctl=json.dumps({"devices": DEVICES}), destinations=HEADER + DESTINATION)
        candidate, _ = self.frozen_probe(outputs)
        self.assertEqual(candidate["eligible_simulators"], [])
        self.assertIn("No available", candidate["error"])

    def test_wrong_version_build_or_sdk_is_rejected(self):
        for field, value in (("version", "Xcode 16.4\nBuild version 16F6\n"),
                             ("version", "Xcode 26.2.1\nBuild version 17C52\n"),
                             ("version", "Xcode 26.2\nBuild version 17C53\n"),
                             ("version", "Xcode 26.2\nBuild version 16F6\n"),
                             ("sdk", "18.5\n"), ("sdk", "26.2.1\n")):
            with self.subTest(field=field, value=value):
                candidate, _ = self.frozen_probe(dict(self.inventory(), **{field: value}))
                self.assertIn("Frozen selection requires exactly", candidate["error"])
                self.assertEqual(candidate["eligible_simulators"], [])

    def test_other_host_architecture_or_developer_path_is_not_probed(self):
        for architecture, path in (("x86_64", selector.FROZEN_DEVELOPER_DIR),
                                   ("arm64e", selector.FROZEN_DEVELOPER_DIR),
                                   ("arm64", PREVIOUS_DEVELOPER_DIR)):
            with self.subTest(architecture=architecture, path=path):
                candidate, calls = self.frozen_probe(self.inventory(), architecture, path)
                self.assertIn("fixed installed Xcode 26.2 path and an arm64 host", candidate["error"])
                self.assertEqual(calls, [])

    def test_missing_installed_toolchain_does_not_probe_or_install(self):
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(Path, "is_dir", return_value=False), \
                patch.object(selector.subprocess, "run") as run:
            candidate = selector.probe(selector.FROZEN_DEVELOPER_DIR, "xcode-26.2", Path(directory),
                                       "arm64", freeze_xcode_26_2=True)
            self.assertIn("not already installed", candidate["error"])
            run.assert_not_called()

    def test_each_inventory_must_identify_the_se3_model(self):
        variants = []
        for changes in ({"modelCode": "iPhone17,1"}, {"modelCode": None}, {"name": "iPhone 16"}):
            rows = [dict(json.loads(self.inventory()["xcdevice"])[0], **changes)]
            variants.append(dict(self.inventory(), xcdevice=json.dumps(rows)))
        for name in ("simctl", "destinations"):
            outputs = self.inventory()
            outputs[name] = outputs[name].replace("iPhone SE (3rd generation)", "iPhone 16")
            variants.append(outputs)
        for outputs in variants:
            with self.subTest(outputs=outputs):
                candidate, _ = self.frozen_probe(outputs)
                self.assertIn("No available", candidate["error"])
                self.assertEqual(candidate["eligible_simulators"], [])

    def test_no_three_way_intersection_or_available_device_is_rejected(self):
        variants = [dict(self.inventory(), xcdevice="[]"),
                    dict(self.inventory(), simctl='{"devices": {}}'),
                    dict(self.inventory(), destinations=PLACEHOLDERS)]
        for name in ("simctl", "xcdevice", "destinations"):
            outputs = self.inventory()
            outputs[name] = outputs[name].replace(FROZEN_ID, "00000000-0000-0000-0000-000000000001")
            variants.append(outputs)
        for name, field in (("simctl", "isAvailable"), ("xcdevice", "available")):
            outputs = self.inventory()
            outputs[name] = outputs[name].replace(f'"{field}": true', f'"{field}": false')
            variants.append(outputs)
        for outputs in variants:
            with self.subTest(outputs=outputs):
                candidate, _ = self.frozen_probe(outputs)
                self.assertIn("No available", candidate["error"])
                self.assertEqual(candidate["eligible_simulators"], [])

    def test_frozen_cli_only_probes_fixed_path_and_exports_current_observed_uuid(self):
        for identifier in (FROZEN_ID, "f107b59b-9864-4ae7-a12a-9104c143c89d"):
            with self.subTest(identifier=identifier), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "mobile/ios/Pods/Pods.xcodeproj").mkdir(parents=True)
                github_env = root / "github-env"
                github_env.write_text("EXISTING=value\n")
                original_is_dir = Path.is_dir
                with patch.object(selector, "ROOT", root), \
                        patch.object(Path, "is_dir", autospec=True, side_effect=lambda path:
                                     path == selector.FROZEN_DEVELOPER_DIR or original_is_dir(path)), \
                        patch.object(selector.platform, "system", return_value="Darwin"), \
                        patch.object(selector.platform, "machine", return_value="arm64"), \
                        patch.dict(os.environ, {"GITHUB_ENV": str(github_env), "DEVELOPER_DIR": str(PREVIOUS_DEVELOPER_DIR)}), \
                        patch.object(selector.subprocess, "check_output") as current_xcode, \
                        patch.object(selector.subprocess, "run", side_effect=self.response(self.inventory(identifier))) as run, \
                        redirect_stdout(io.StringIO()):
                    selector.main(["--freeze-xcode-26.2"])
                    self.assertEqual(os.environ["DEVELOPER_DIR"], str(PREVIOUS_DEVELOPER_DIR))
                current_xcode.assert_not_called()
                self.assertEqual(run.call_count, 5)
                for call in run.call_args_list:
                    self.assertEqual(call.kwargs["env"]["DEVELOPER_DIR"], str(selector.FROZEN_DEVELOPER_DIR))
                self.assertEqual(github_env.read_text(), "EXISTING=value\n" +
                                 f"DEVELOPER_DIR={selector.FROZEN_DEVELOPER_DIR}\nIOTOOLS_IOS_SIMULATOR_UDID={identifier}\n")
                report = json.loads((root / "platform-evidence/ios/xcode-selection.json").read_text())
                self.assertEqual(report["selection_mode"], "freeze-xcode-26.2")
                self.assertEqual(len(report["candidates"]), 1)
                self.assertEqual(report["simulator"]["udid"], identifier)
                self.assertTrue(report["github_environment_written"])

    def test_frozen_cli_requires_job_environment_and_never_falls_back(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "mobile/ios/Pods/Pods.xcodeproj").mkdir(parents=True)
            with patch.object(selector, "ROOT", root), \
                    patch.object(selector.platform, "system", return_value="Darwin"), \
                    patch.dict(os.environ, {}, clear=True), \
                    patch.object(selector.subprocess, "check_output") as current_xcode, \
                    patch.object(selector.subprocess, "run") as run, redirect_stdout(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, "requires GITHUB_ENV"):
                    selector.main(["--freeze-xcode-26.2"])
            current_xcode.assert_not_called()
            run.assert_not_called()
            report = json.loads((root / "platform-evidence/ios/xcode-selection.json").read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["candidates"], [])
        candidate, _ = self.frozen_probe(dict(self.inventory(), xcdevice="[]"))
        with self.assertRaisesRegex(RuntimeError, "no fallback is allowed"):
            selector.choose_candidate([candidate], freeze_xcode_26_2=True)

    def test_failed_frozen_cli_probe_never_tries_another_xcode_or_exports_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "mobile/ios/Pods/Pods.xcodeproj").mkdir(parents=True)
            github_env = root / "github-env"
            github_env.write_text("EXISTING=value\n")
            outputs = dict(self.inventory(), version="Xcode 16.4\nBuild version 16F6\n")
            with patch.object(selector, "ROOT", root), \
                    patch.object(Path, "is_dir", return_value=True), \
                    patch.object(selector.platform, "system", return_value="Darwin"), \
                    patch.object(selector.platform, "machine", return_value="arm64"), \
                    patch.dict(os.environ, {"GITHUB_ENV": str(github_env), "DEVELOPER_DIR": str(PREVIOUS_DEVELOPER_DIR)}), \
                    patch.object(selector.subprocess, "check_output") as current_xcode, \
                    patch.object(selector.subprocess, "run", side_effect=self.response(outputs)) as run, \
                    redirect_stdout(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, "no fallback is allowed"):
                    selector.main(["--freeze-xcode-26.2"])
            current_xcode.assert_not_called()
            self.assertEqual(run.call_count, 5)
            for call in run.call_args_list:
                self.assertEqual(call.kwargs["env"]["DEVELOPER_DIR"], str(selector.FROZEN_DEVELOPER_DIR))
            self.assertEqual(github_env.read_text(), "EXISTING=value\n")
            report = json.loads((root / "platform-evidence/ios/xcode-selection.json").read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual(len(report["candidates"]), 1)
            self.assertNotIn("simulator", report)


class ColdHostDiscoveryRecoveryTests(unittest.TestCase):
    inventory = FrozenXcodeSelectionTests.inventory

    def observe(self, observations, budget=300, durations=None, freeze=True):
        """Script whole observations with a deterministic monotonic/wall clock."""
        commands = selector.probe_commands("arm64")
        elapsed = [0.0]
        calls = []
        base = datetime(2026, 10, 3, tzinfo=timezone.utc)
        durations = durations or {}

        def run(command, **kwargs):
            observation = len(calls) // 5
            name = next(name for name, expected in commands.items() if command == expected)
            calls.append((name, command, kwargs))
            value = observations[observation][name]
            duration = durations.get((observation, name), 1)
            if isinstance(value, subprocess.TimeoutExpired):
                elapsed[0] += kwargs["timeout"]
                raise subprocess.TimeoutExpired(command, kwargs["timeout"], output=value.stdout, stderr=value.stderr)
            if duration >= kwargs["timeout"]:
                elapsed[0] += kwargs["timeout"]
                raise subprocess.TimeoutExpired(command, kwargs["timeout"])
            elapsed[0] += duration
            if isinstance(value, subprocess.CompletedProcess):
                return value
            return subprocess.CompletedProcess(command, 0, value, f"observation {observation + 1}: {name} stderr\n")

        with tempfile.TemporaryDirectory() as directory, \
                patch.object(Path, "is_dir", return_value=True), \
                patch.object(selector, "DISCOVERY_BUDGET_SECONDS", budget), \
                patch.object(selector.time, "monotonic", side_effect=lambda: elapsed[0]), \
                patch.object(selector, "timestamp", side_effect=lambda: (base + timedelta(seconds=elapsed[0])).isoformat()), \
                patch.object(selector, "datetime", wraps=datetime) as date, \
                patch.object(selector.subprocess, "run", side_effect=run):
            date.now.return_value = base
            candidate = selector.probe(selector.FROZEN_DEVELOPER_DIR, "xcode-26.2", Path(directory),
                                       "arm64", freeze_xcode_26_2=freeze)
            logs = {path.name: path.read_text() for path in Path(directory).iterdir()}
        return candidate, calls, logs

    def cold_inventory(self):
        return dict(self.inventory(), xcdevice=json.dumps(ACTUAL_622_CURRENT_XCDEVICES),
                    simctl=subprocess.TimeoutExpired([], 30, output=b"partial simctl", stderr=b"starting CoreSimulator"))

    def test_cold_host_gets_one_fresh_observation_and_preserves_first_failure(self):
        fresh_id = "f107b59b-9864-4ae7-a12a-9104c143c89d"
        original_environment = dict(os.environ)
        candidate, calls, logs = self.observe([self.cold_inventory(), self.inventory(fresh_id)])
        self.assertNotIn("error", candidate)
        self.assertTrue(candidate["resampled"])
        self.assertEqual(candidate["eligible_simulators"][0]["udid"], fresh_id)
        self.assertEqual(candidate["duration_seconds"], 39)
        self.assertEqual(candidate["total_budget_seconds"], 300)
        self.assertEqual(candidate["deadline_at"], "2026-10-03T00:05:00+00:00")
        self.assertEqual(candidate["first_failure"]["error"], "Read-only discovery failed: simctl")
        self.assertIn("Mac-only", candidate["first_failure"]["startup_retry_reason"])
        self.assertEqual(candidate["first_failure"]["duration_seconds"], 34)
        self.assertEqual(candidate["first_failure"]["started_at"], "2026-10-03T00:00:00+00:00")
        self.assertEqual(candidate["first_failure"]["finished_at"], "2026-10-03T00:00:34+00:00")
        self.assertEqual(len(candidate["attempts"]), 2)
        self.assertEqual(len(calls), 10)
        self.assertEqual([kwargs["timeout"] for _, _, kwargs in calls], [30, 30, 30, 30, 90] * 2)
        self.assertEqual(len(logs), 20)
        self.assertEqual(logs["xcode-26.2-simctl.txt"], "partial simctl")
        self.assertIn("starting CoreSimulator", logs["xcode-26.2-simctl-stderr.txt"])
        self.assertIn("timed out after 30", logs["xcode-26.2-simctl-stderr.txt"])
        self.assertEqual(json.loads(logs["xcode-26.2-xcdevice.txt"]), ACTUAL_622_CURRENT_XCDEVICES)
        self.assertIn(fresh_id, logs["xcode-26.2-retry1-xcdevice.txt"])
        self.assertIn("observation 1", logs["xcode-26.2-destinations-stderr.txt"])
        self.assertIn("observation 2", logs["xcode-26.2-retry1-destinations-stderr.txt"])
        for attempt in candidate["attempts"]:
            for query in attempt["queries"].values():
                self.assertLessEqual(datetime.fromisoformat(query["started_at"]), datetime.fromisoformat(query["finished_at"]))
                self.assertGreaterEqual(query["duration_seconds"], 0)
        for _, command, kwargs in calls:
            self.assertEqual(kwargs["env"]["DEVELOPER_DIR"], str(selector.FROZEN_DEVELOPER_DIR))
            self.assertEqual(command[0], "xcrun")
        self.assertEqual(dict(os.environ), original_environment)

    def test_exact_a99_startup_shape_recovers_only_from_complete_fresh_second_pass(self):
        first = dict(ACTUAL_A99_STARTUP_OUTPUTS, simctl=subprocess.TimeoutExpired([], 30, output=b""))
        fresh_id = "f107b59b-9864-4ae7-a12a-9104c143c89d"
        candidate, calls, logs = self.observe([first, self.inventory(fresh_id)])
        self.assertNotIn("error", candidate)
        self.assertEqual(len(calls), 10)
        self.assertEqual(candidate["eligible_simulators"][0]["udid"], fresh_id)
        self.assertEqual(candidate["first_failure"]["error"], "Read-only discovery failed: simctl")
        self.assertIn("only the generic iOS Simulator placeholder", candidate["first_failure"]["startup_retry_reason"])
        for name, output in ACTUAL_A99_STARTUP_OUTPUTS.items():
            self.assertEqual(logs[f"xcode-26.2-{name}.txt"], output)
        self.assertEqual(logs["xcode-26.2-simctl.txt"], "")
        for second in (first, dict(self.inventory(fresh_id), destinations=ACTUAL_A99_STARTUP_OUTPUTS["destinations"])):
            with self.subTest(second=second):
                candidate, calls, _ = self.observe([first, second])
                self.assertIn("error", candidate)
                self.assertEqual(candidate["eligible_simulators"], [])
                self.assertEqual(len(calls), 10)

    def test_only_exact_valid_placeholder_is_startup_absence_not_contrary_evidence(self):
        concrete = HEADER + ACTUAL_622_26_DESTINATION
        bad_destinations = [HEADER, "", PLACEHOLDERS.replace(selector.SIMULATOR_PLACEHOLDER, "unknown:placeholder"),
                            PLACEHOLDERS.replace("Any iOS Simulator Device", "Unknown device"),
                            PLACEHOLDERS.replace(" }", ", error:unavailable }"),
                            PLACEHOLDERS.replace(" }", ""),
                            concrete.replace("iPhone SE (3rd generation)", "iPhone 16"),
                            concrete.replace("OS:26.2", "OS:18.5"),
                            PLACEHOLDERS + concrete.replace("iPhone SE (3rd generation)", "iPhone 16")]
        for destinations in bad_destinations:
            with self.subTest(destinations=destinations):
                first = dict(ACTUAL_A99_STARTUP_OUTPUTS, destinations=destinations,
                             simctl=subprocess.TimeoutExpired([], 30))
                candidate, calls, _ = self.observe([first, self.inventory()])
                self.assertIn("error", candidate)
                self.assertFalse(candidate["resampled"])
                self.assertEqual(len(calls), 5)

    def test_valid_empty_or_mac_only_xcdevice_can_recover_without_timeout(self):
        for rows in ([], ACTUAL_622_CURRENT_XCDEVICES):
            with self.subTest(rows=rows):
                candidate, calls, _ = self.observe([dict(self.inventory(), xcdevice=json.dumps(rows)), self.inventory()])
                self.assertNotIn("error", candidate)
                self.assertTrue(candidate["resampled"])
                self.assertEqual(len(calls), 10)

    def test_each_discovery_timeout_is_retryable_but_no_other_command_is(self):
        for name in selector.probe_commands("arm64"):
            with self.subTest(name=name):
                first = dict(self.inventory(), **{name: subprocess.TimeoutExpired([], 30)})
                candidate, calls, _ = self.observe([first, self.inventory()])
                expected_retry = name in selector.DISCOVERY_QUERIES
                self.assertEqual(candidate["resampled"], expected_retry)
                self.assertEqual(len(calls), 10 if expected_retry else 5)
                self.assertEqual("error" in candidate, not expected_retry)

    def test_repeated_startup_failure_stops_after_second_complete_observation(self):
        candidate, calls, _ = self.observe([self.cold_inventory(), self.cold_inventory()])
        self.assertIn("Read-only discovery failed: simctl", candidate["error"])
        self.assertEqual(candidate["eligible_simulators"], [])
        self.assertTrue(candidate["resampled"])
        self.assertEqual(len(calls), 10)
        with self.assertRaisesRegex(RuntimeError, "no fallback"):
            selector.choose_candidate([candidate], freeze_xcode_26_2=True)

    def test_source_conflicts_do_not_retry_even_with_a_startup_timeout(self):
        other = "00000000-0000-0000-0000-000000000001"
        cases = [dict(self.inventory(), xcdevice=self.inventory(other)["xcdevice"]),
                 dict(self.inventory(), xcdevice="[]", destinations=self.inventory(other)["destinations"]),
                 dict(self.inventory(), xcdevice=self.inventory(other)["xcdevice"],
                      destinations=subprocess.TimeoutExpired([], 90))]
        for first in cases:
            with self.subTest(first=first):
                candidate, calls, _ = self.observe([first, self.inventory()])
                self.assertIn("error", candidate)
                self.assertFalse(candidate["resampled"])
                self.assertEqual(len(calls), 5)

    def test_second_observation_cannot_reuse_first_observations_matching_uuid(self):
        second = dict(self.inventory("00000000-0000-0000-0000-000000000001"),
                      destinations=self.inventory()["destinations"])
        candidate, calls, _ = self.observe([self.cold_inventory(), second])
        self.assertIn("No available", candidate["error"])
        self.assertEqual(candidate["eligible_simulators"], [])
        self.assertEqual(len(calls), 10)

    def test_malformed_or_ambiguous_successful_evidence_never_retries(self):
        valid = self.inventory()
        bad = [("xcdevice", "{bad"), ("xcdevice", "[null]"), ("xcdevice", "[{}]"),
               ("simctl", "{bad"), ("simctl", '{"devices": []}'),
               ("simctl", '{"devices": {"runtime": [null]}}'),
               ("destinations", "not destinations"),
               ("xcdevice", json.dumps(json.loads(valid["xcdevice"]) * 2)),
               ("destinations", valid["destinations"] + valid["destinations"].splitlines()[1])]
        for name, value in bad:
            with self.subTest(name=name, value=value):
                first = dict(valid, **{name: value})
                timeout_source = "simctl" if name != "simctl" else "xcdevice"
                first[timeout_source] = subprocess.TimeoutExpired([], 30)
                candidate, calls, _ = self.observe([first, valid])
                self.assertIn("Invalid discovery evidence", candidate["error"])
                self.assertFalse(candidate["resampled"])
                self.assertEqual(len(calls), 5)

    def test_generic_nonzero_exit_or_wrong_frozen_target_never_retries(self):
        cases = [dict(self.cold_inventory(), xcdevice=subprocess.CompletedProcess([], 1, "", "unknown failure")),
                 dict(self.cold_inventory(), destinations=self.inventory()["destinations"].replace("iPhone SE (3rd generation)", "iPhone 16")),
                 dict(self.inventory(), simctl=subprocess.TimeoutExpired([], 30),
                      xcdevice=self.inventory()["xcdevice"].replace("iPhone14,6", "iPhone17,1"))]
        for first in cases:
            with self.subTest(first=first):
                candidate, calls, _ = self.observe([first, self.inventory()])
                self.assertIn("error", candidate)
                self.assertFalse(candidate["resampled"])
                self.assertEqual(len(calls), 5)

    def test_wrong_toolchain_or_sdk_never_retries_and_revalidated_on_second_pass(self):
        for field, value in (("version", "Xcode 16.4\nBuild version 16F6\n"), ("sdk", "18.5\n")):
            with self.subTest(field=field):
                first = dict(self.cold_inventory(), **{field: value})
                candidate, calls, _ = self.observe([first, self.inventory()])
                self.assertIn("Frozen selection requires exactly", candidate["error"])
                self.assertFalse(candidate["resampled"])
                self.assertEqual(len(calls), 5)
                candidate, calls, _ = self.observe([self.cold_inventory(), dict(self.inventory(), **{field: value})])
                self.assertIn("Frozen selection requires exactly", candidate["error"])
                self.assertEqual(candidate["eligible_simulators"], [])
                self.assertEqual(len(calls), 10)
                self.assertTrue(all(kwargs["env"]["DEVELOPER_DIR"] == str(selector.FROZEN_DEVELOPER_DIR)
                                    for _, _, kwargs in calls))

    def test_remaining_total_budget_shortens_timeout_and_rejects_late_result(self):
        candidate, calls, _ = self.observe([self.cold_inventory(), self.inventory()], budget=40,
                                           durations={(1, "destinations"): 10})
        self.assertIn("total deadline exhausted", candidate["error"])
        self.assertEqual(candidate["duration_seconds"], 40)
        self.assertEqual(candidate["eligible_simulators"], [])
        self.assertEqual(len(calls), 10)
        self.assertEqual(calls[-1][2]["timeout"], 2)
        self.assertEqual(candidate["queries"]["destinations"]["original_timeout_seconds"], 90)
        self.assertTrue(candidate["queries"]["destinations"]["deadline_exhausted"])

    def test_no_command_starts_after_total_budget_is_consumed(self):
        candidate, calls, logs = self.observe([self.cold_inventory(), self.inventory()], budget=35,
                                             durations={(1, "version"): 2})
        self.assertIn("total deadline exhausted", candidate["error"])
        self.assertEqual(len(calls), 6)
        self.assertEqual(candidate["duration_seconds"], 35)
        self.assertEqual(candidate["eligible_simulators"], [])
        for name in ("sdk", "xcdevice", "simctl", "destinations"):
            self.assertTrue(candidate["queries"][name]["skipped"])
            self.assertEqual(candidate["queries"][name]["timeout_seconds"], 0)
            self.assertIn("command not run", logs[f"xcode-26.2-retry1-{name}-stderr.txt"])

    def test_cli_recovery_only_exports_final_complete_success_and_retains_report(self):
        fresh_id = "f107b59b-9864-4ae7-a12a-9104c143c89d"
        for recover in (True, False):
            with self.subTest(recover=recover), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "mobile/ios/Pods/Pods.xcodeproj").mkdir(parents=True)
                github_env = root / "github-env"
                github_env.write_text("EXISTING=value\n")
                first = self.cold_inventory()
                observations = [first, self.inventory(fresh_id) if recover else first]
                calls = []

                def run(command, **kwargs):
                    observation = len(calls) // 5
                    calls.append((command, kwargs))
                    commands = selector.probe_commands("arm64")
                    name = next(name for name, expected in commands.items() if command == expected)
                    value = observations[observation][name]
                    if isinstance(value, subprocess.TimeoutExpired):
                        raise value
                    # Xcode can put the destinations inventory on stderr.
                    return subprocess.CompletedProcess(command, 0, "" if name == "destinations" else value,
                                                       value if name == "destinations" else "")

                with patch.object(selector, "ROOT", root), \
                        patch.object(Path, "is_dir", return_value=True), \
                        patch.object(selector.platform, "system", return_value="Darwin"), \
                        patch.object(selector.platform, "machine", return_value="arm64"), \
                        patch.dict(os.environ, {"GITHUB_ENV": str(github_env), "DEVELOPER_DIR": str(PREVIOUS_DEVELOPER_DIR)}), \
                        patch.object(selector.subprocess, "check_output") as current_xcode, \
                        patch.object(selector.subprocess, "run", side_effect=run), redirect_stdout(io.StringIO()):
                    if recover:
                        selector.main(["--freeze-xcode-26.2"])
                    else:
                        with self.assertRaisesRegex(RuntimeError, "no fallback"):
                            selector.main(["--freeze-xcode-26.2"])
                    self.assertEqual(os.environ["DEVELOPER_DIR"], str(PREVIOUS_DEVELOPER_DIR))
                current_xcode.assert_not_called()
                self.assertEqual(len(calls), 10)
                report = json.loads((root / "platform-evidence/ios/xcode-selection.json").read_text())
                self.assertEqual(report["status"], "selected" if recover else "failed")
                self.assertEqual(len(report["candidates"]), 1)
                self.assertEqual(len(report["candidates"][0]["attempts"]), 2)
                self.assertIn("first_failure", report["candidates"][0])
                expected = "EXISTING=value\n"
                if recover:
                    expected += f"DEVELOPER_DIR={selector.FROZEN_DEVELOPER_DIR}\nIOTOOLS_IOS_SIMULATOR_UDID={fresh_id}\n"
                    self.assertEqual(report["simulator"]["udid"], fresh_id)
                else:
                    self.assertNotIn("simulator", report)
                self.assertEqual(github_env.read_text(), expected)

    def test_default_mode_remains_one_observation_without_startup_retry(self):
        candidate, calls, _ = self.observe([self.cold_inventory(), self.inventory()], freeze=False)
        self.assertFalse(candidate["resampled"])
        self.assertIn("error", candidate)
        self.assertEqual(len(calls), 5)


if __name__ == "__main__":
    unittest.main()
