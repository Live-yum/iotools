#!/usr/bin/env python3
"""Probe installed Xcodes without building, booting, installing or global changes.

Run after Flutter's iOS --config-only preparation. Select the current Xcode when
eligible, otherwise the already-installed Xcode 26.2. simctl, xcdevice and
xcodebuild must all list the same available, SDK/architecture-matched simulator.
Pass --freeze-xcode-26.2 to require the installed 26.2/17C52 toolchain, iOS 26.2
SDK and an arm64 iPhone SE (3rd generation), with no fallback.
"""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import uuid

ROOT = Path(__file__).resolve().parents[2]
ALTERNATE = Path("/Applications/Xcode_26.2.app/Contents/Developer")
FROZEN_DEVELOPER_DIR = Path("/Applications/Xcode_26.2.app/Contents/Developer")
FROZEN_MODEL_NAME = "iPhone SE (3rd generation)"


def release(version):
    match = re.fullmatch(r"(\d+)\.(\d+)(?:\.\d+)?", version)
    if not match:
        raise ValueError(f"Unexpected SDK/runtime version: {version}")
    return tuple(int(value) for value in match.groups())


def eligible_simulators(destinations, devices, xcdevices, sdk_version, architecture):
    """Require agreement from all three discovery sources for a concrete iPhone."""
    sdk_release = release(sdk_version)
    if not isinstance(xcdevices, list) or any(not isinstance(row, dict) for row in xcdevices):
        raise ValueError("xcdevice did not return a device list")
    xcdevice_simulators = {}
    for device in xcdevices:
        if (device.get("simulator") is not True or device.get("available") is not True
                or device.get("ignored", False) is not False
                or device.get("platform") != "com.apple.platform.iphonesimulator"
                or device.get("architecture") != architecture):
            continue
        version = re.fullmatch(r"(\d+\.\d+(?:\.\d+)?)(?: \([^()]+\))?",
                               device.get("operatingSystemVersion", ""))
        try:
            identifier = str(uuid.UUID(device.get("identifier", "")))
            if not version or release(version[1]) != sdk_release:
                continue
        except ValueError:
            continue
        xcdevice_simulators[identifier] = device
    listed = {}
    available_section = False
    for line in destinations.splitlines():
        if line.strip().startswith("Available destinations for "):
            available_section = True
            continue
        if line.strip().startswith("Ineligible destinations for "):
            available_section = False
            continue
        if not available_section or not line.strip().startswith("{"):
            continue
        fields = dict(re.findall(r"(?:\{|,)\s*([A-Za-z_]+):\s*([^,}]*)", line))
        fields = {key: value.strip() for key, value in fields.items()}
        if fields.get("platform") != "iOS Simulator" or fields.get("arch") != architecture:
            continue
        try:
            identifier = str(uuid.UUID(fields.get("id", "")))
            if release(fields.get("OS", "")) != sdk_release:
                continue
        except ValueError:
            continue
        if "error" not in fields:
            listed[identifier] = fields
    eligible = []
    for runtime, rows in devices.items():
        version = re.fullmatch(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+)-(\d+)(?:-\d+)?", runtime)
        if not version or tuple(int(value) for value in version.groups()) != sdk_release:
            continue
        for device in rows:
            identifier = device.get("udid", "").lower()
            if (identifier in listed and identifier in xcdevice_simulators
                    and device.get("isAvailable") is True and device.get("name", "").startswith("iPhone")):
                eligible.append({"udid": identifier, "name": device["name"], "runtime": runtime,
                                 "state": device.get("state"), "xcode_destination": listed[identifier],
                                 "xcdevice": xcdevice_simulators[identifier]})
    eligible.sort(key=lambda row: (row["state"] == "Booted", row["name"], row["udid"]), reverse=True)
    return eligible


def probe_commands(architecture):
    if architecture not in ("arm64", "x86_64"):
        raise ValueError(f"Unsupported simulator architecture: {architecture}")
    return {
        "version": ["xcrun", "xcodebuild", "-version"],
        "sdk": ["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"],
        "xcdevice": ["xcrun", "xcdevice", "list", "--timeout", "10"],
        "simctl": ["xcrun", "simctl", "list", "devices", "available", "--json"],
        "destinations": [
            "xcrun", "xcodebuild", "-workspace", str(ROOT / "mobile/ios/Runner.xcworkspace"),
            "-scheme", "Runner", "-configuration", "Debug", "-sdk", "iphonesimulator",
            f"ARCHS={architecture}", "ONLY_ACTIVE_ARCH=YES", "CODE_SIGNING_ALLOWED=NO",
            "CODE_SIGNING_REQUIRED=NO", "CODE_SIGN_IDENTITY=", "-showdestinations",
        ],
    }


def probe(developer_dir, label, evidence, architecture, freeze_xcode_26_2=False):
    candidate = {"developer_dir": str(developer_dir), "queries": {}, "eligible_simulators": []}
    if freeze_xcode_26_2 and (developer_dir != FROZEN_DEVELOPER_DIR or architecture != "arm64"):
        candidate["error"] = "Frozen selection requires the fixed installed Xcode 26.2 path and an arm64 host"
        return candidate
    if not developer_dir.is_dir():
        candidate["error"] = "Xcode is not already installed at this path"
        return candidate
    environment = dict(os.environ, DEVELOPER_DIR=str(developer_dir))
    outputs = {}
    for name, command in probe_commands(architecture).items():
        stdout_path = evidence / f"{label}-{name}.txt"
        stderr_path = evidence / f"{label}-{name}-stderr.txt"
        timeout = 90 if name == "destinations" else 30
        try:
            result = subprocess.run(command, env=environment, text=True, capture_output=True, timeout=timeout)
            stdout, stderr, exit_code = result.stdout, result.stderr, result.returncode
        except subprocess.TimeoutExpired as error:
            stdout = error.stdout or b""
            stderr = error.stderr or b""
            stdout = stdout.decode(errors="replace") if isinstance(stdout, bytes) else stdout
            stderr = stderr.decode(errors="replace") if isinstance(stderr, bytes) else stderr
            stderr += f"\nRead-only discovery timed out after {timeout} seconds\n"
            exit_code = None
        stdout_path.write_text(stdout)
        stderr_path.write_text(stderr)
        outputs[name] = stdout + "\n" + stderr if name == "destinations" else stdout
        candidate["queries"][name] = {"command": command, "exit_code": exit_code,
                                      "stdout": stdout_path.name, "stderr": stderr_path.name}
    failed = [name for name, result in candidate["queries"].items() if result["exit_code"] != 0]
    if failed:
        candidate["error"] = f"Read-only discovery failed: {', '.join(failed)}"
        return candidate
    try:
        candidate["xcode_version"] = outputs["version"].strip()
        candidate["simulator_sdk"] = outputs["sdk"].strip()
        if not re.match(r"^Xcode \d+(?:\.\d+)*\nBuild version \S+", candidate["xcode_version"]):
            raise ValueError("xcodebuild did not report an Xcode version and build")
        if not freeze_xcode_26_2 and label == "xcode-26.2" and candidate["xcode_version"].splitlines()[:1] != ["Xcode 26.2"]:
            raise ValueError("The fixed Xcode 26.2 path did not report Xcode 26.2")
        if freeze_xcode_26_2:
            if candidate["xcode_version"] != "Xcode 26.2\nBuild version 17C52":
                raise ValueError("Frozen selection requires exactly Xcode 26.2 build 17C52")
            if candidate["simulator_sdk"] != "26.2":
                raise ValueError("Frozen selection requires exactly the iOS simulator SDK 26.2")
        candidate["eligible_simulators"] = eligible_simulators(
            outputs["destinations"], json.loads(outputs["simctl"])["devices"],
            json.loads(outputs["xcdevice"]), candidate["simulator_sdk"], architecture)
        if freeze_xcode_26_2:
            candidate["eligible_simulators"] = [
                device for device in candidate["eligible_simulators"]
                if device["name"] == FROZEN_MODEL_NAME
                and device["xcode_destination"].get("name") == FROZEN_MODEL_NAME
                and device["xcdevice"].get("name") == FROZEN_MODEL_NAME
                and device["xcdevice"].get("modelCode") == "iPhone14,6"
            ]
        if not candidate["eligible_simulators"]:
            model = FROZEN_MODEL_NAME if freeze_xcode_26_2 else "iPhone"
            candidate["error"] = f"No available SDK/architecture-matched {model} agrees across simctl, xcdevice and xcodebuild destinations"
    except (ValueError, KeyError, TypeError) as error:
        candidate["error"] = f"Invalid discovery evidence: {error}"
    return candidate


def choose_candidate(candidates, freeze_xcode_26_2=False):
    for candidate in candidates:
        if not candidate.get("error") and candidate.get("eligible_simulators"):
            return candidate
    if freeze_xcode_26_2:
        raise RuntimeError("The frozen Xcode 26.2/17C52, SDK 26.2, arm64 iPhone SE (3rd generation) selection is unavailable; no fallback is allowed; see xcode-selection.json and discovery logs")
    raise RuntimeError("Neither the current Xcode nor installed Xcode 26.2 exposes an eligible simulator; see xcode-selection.json and discovery logs")


def write_environment(path, selected):
    values = {"DEVELOPER_DIR": selected["developer_dir"],
              "IOTOOLS_IOS_SIMULATOR_UDID": selected["eligible_simulators"][0]["udid"]}
    if any("\n" in value or "\r" in value for value in values.values()):
        raise ValueError("Refusing multiline GitHub environment values")
    with path.open("a") as output:
        for key, value in values.items():
            output.write(f"{key}={value}\n")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--freeze-xcode-26.2", dest="freeze_xcode_26_2", action="store_true",
                        help="Require installed Xcode 26.2/17C52, SDK 26.2 and an arm64 SE3; write the observed UUID to GITHUB_ENV without fallback")
    args = parser.parse_args(argv)
    if platform.system() != "Darwin":
        raise RuntimeError("Installed Xcode discovery requires a macOS runner")
    if not (ROOT / "mobile/ios/Pods/Pods.xcodeproj").is_dir():
        raise RuntimeError("Prepare Flutter iOS dependencies with flutter build ios --simulator --debug --no-codesign --config-only first")
    evidence = ROOT / "platform-evidence/ios"
    evidence.mkdir(parents=True, exist_ok=True)
    if args.freeze_xcode_26_2:
        candidates = [("xcode-26.2", FROZEN_DEVELOPER_DIR)]
    else:
        current = os.environ.get("DEVELOPER_DIR") or subprocess.check_output(["xcode-select", "-p"], text=True, timeout=30).strip()
        candidates = [("current", Path(current)), ("xcode-26.2", ALTERNATE)]
    report = {"source_sha": os.environ.get("IOTOOLS_SHA", "local"),
              "observed_at": datetime.now(timezone.utc).isoformat(), "status": "running", "candidates": [],
              "selection_mode": "freeze-xcode-26.2" if args.freeze_xcode_26_2 else "default"}
    try:
        if args.freeze_xcode_26_2 and not os.environ.get("GITHUB_ENV"):
            raise RuntimeError("Frozen selection requires GITHUB_ENV to preserve the toolchain and observed simulator UUID for this job")
        seen = set()
        for label, path in candidates:
            if not args.freeze_xcode_26_2:
                path = path.resolve()
            if path in seen:
                continue
            seen.add(path)
            report["candidates"].append(probe(path, label, evidence, platform.machine(), args.freeze_xcode_26_2))
        selected = choose_candidate(report["candidates"], args.freeze_xcode_26_2)
        report.update(status="selected", developer_dir=selected["developer_dir"],
                      simulator_sdk=selected["simulator_sdk"], simulator=selected["eligible_simulators"][0])
        github_env = os.environ.get("GITHUB_ENV")
        if github_env:
            write_environment(Path(github_env), selected)
        report["github_environment_written"] = bool(github_env)
    except Exception as error:
        report.update(status="failed", error=str(error))
        raise
    finally:
        (evidence / "xcode-selection.json").write_text(json.dumps(report, indent=2))
        print(json.dumps(report))


if __name__ == "__main__":
    main()
