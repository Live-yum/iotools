"""One build-for-testing and one real XCTest run from its unchanged descriptor.

Apple TN2339 documents the split; xcodebuild(1) documents generic build
destinations. Products remain outside evidence and the descriptor is executed
in its original directory, preserving Apple's __TESTROOT__ semantics.
"""

import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import time

EXPECTED_METHODS = frozenset({
    "testExportLimitsAndUTF8", "testExportPrivateBoundaryAndSize",
    "testCancelledExportCleansStaging", "testRealExportPickerCancelBusyAndStaleDelegate",
    "testActualProcessGoABIAndLifecycle",
})
PINNED_DEVELOPER = "/Applications/Xcode_26.2.app/Contents/Developer"
COMMITTED_INPUTS = (
    "go.mod", "go.sum", "cmd/iotools-native", "internal", "mobile/lib", "mobile/assets",
    "mobile/pubspec.yaml", "mobile/pubspec.lock", "mobile/ios/RunnerTests",
    "mobile/ios/Runner/AppDelegate.swift", "mobile/ios/Runner/Info.plist",
    "mobile/ios/Runner/Runner-Bridging-Header.h", "mobile/ios/Runner/Base.lproj",
    "mobile/ios/Podfile", "mobile/ios/Flutter/Debug.xcconfig",
    "mobile/ios/Flutter/Release.xcconfig", "mobile/ios/Flutter/IotoolsNative.xcconfig",
)


def require(value, message):
    if not value:
        raise RuntimeError(message)


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def source_inventory(root):
    files = [root / name for name in ("go.mod", "go.sum", "mobile/pubspec.yaml", "mobile/pubspec.lock",
             "mobile/ios/RunnerTests/RunnerTests.swift", "mobile/ios/Runner/AppDelegate.swift",
             "mobile/ios/Runner.xcodeproj/project.pbxproj",
             "mobile/ios/Runner.xcodeproj/xcshareddata/xcschemes/Runner.xcscheme",
             "mobile/ios/Native/libiotools_native.a")]
    for name in ("mobile/lib", "mobile/assets", "mobile/ios/Runner", "mobile/ios/RunnerTests",
                 "mobile/ios/Native", "cmd/iotools-native", "internal"):
        files.extend(path for path in (root / name).rglob("*") if path.is_file())
    for name in ("Debug.xcconfig", "Release.xcconfig", "IotoolsNative.xcconfig", "Generated.xcconfig"):
        files.append(root / "mobile/ios/Flutter" / name)
    require(all(path.is_file() for path in files), "Host-test source input is missing")
    return {str(path.relative_to(root)): digest(path) for path in sorted(set(files))}


def build_command(settings, products_root, result):
    return [part for part in settings if not part.startswith("BUILD_DIR=")] + [
        "build-for-testing", "-destination", "generic/platform=iOS Simulator",
        "-derivedDataPath", str(products_root),
        f"BUILD_DIR={products_root / 'Build/Products'}",
        f"SYMROOT={products_root / 'Build/Products'}",
        "-resultBundlePath", str(result),
    ]


def test_command(descriptor, udid, result):
    require(re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", udid),
            "Invalid concrete test UUID")
    return ["xcrun", "xcodebuild", "test-without-building", "-xctestrun", str(descriptor),
            "-destination", f"platform=iOS Simulator,id={udid}",
            "-parallel-testing-enabled", "NO", "-only-testing:RunnerTests",
            "-resultBundlePath", str(result)]


def descriptor_target(data):
    require(isinstance(data, dict), "Invalid xctestrun property list")
    version = data.get("__xctestrun_metadata__", {}).get("FormatVersion", 1)
    if version == 1:
        targets = {key: value for key, value in data.items() if key != "__xctestrun_metadata__"}
        require(set(targets) == {"RunnerTests"}, "Expected only RunnerTests in format-1 xctestrun")
        target = targets["RunnerTests"]
    elif version == 2:
        configurations = data.get("TestConfigurations")
        require(isinstance(configurations, list) and len(configurations) == 1,
                "Expected exactly one xctestrun test configuration")
        configuration = configurations[0]
        require(configuration.get("IsEnabled", True) is True, "Disabled xctestrun configuration")
        targets = configuration.get("TestTargets")
        require(isinstance(targets, list) and len(targets) == 1, "Expected one xctestrun target")
        target = targets[0]
        require(target.get("BlueprintName") == "RunnerTests", "Wrong xctestrun test target")
    else:
        raise RuntimeError(f"Unsupported generated xctestrun format: {version}")
    require(isinstance(target, dict), "Invalid xctestrun target")
    require(target.get("BlueprintName", "RunnerTests") == "RunnerTests", "Wrong xctestrun target name")
    require(target.get("ProductModuleName", "RunnerTests") == "RunnerTests", "Wrong xctestrun module")
    for flag in ("IsUITestBundle", "IsSkipped", "UseDestinationArtifacts"):
        require(target.get(flag, False) is False, f"Unexpected xctestrun {flag}")
    require(target.get("IsEnabled", True) is True, "Disabled xctestrun test target")
    require(target.get("IsAppHostedTestBundle", True) is True, "Expected app-hosted XCTest")
    require(not target.get("SkipTestIdentifiers") and not target.get("OnlyTestIdentifiers"),
            "Generated xctestrun restricts the original host tests")
    return target


def descriptor_products(descriptor, products):
    """Read the generated format, resolve documented placeholders, never edit it."""
    target = descriptor_target(plistlib.loads(descriptor.read_bytes()))
    def resolve(value, host=None):
        require(isinstance(value, str) and bool(value), "Missing xctestrun product path")
        value = value.replace("__TESTROOT__", str(descriptor.parent))
        if host is not None:
            value = value.replace("__TESTHOST__", str(host))
        require("__" not in value, "Unsupported xctestrun path placeholder")
        path = Path(value)
        require(path.is_absolute(), "Expected absolute resolved xctestrun product path")
        resolved = path.resolve()
        require(resolved.is_relative_to(products.resolve()), "xctestrun product escapes this build")
        require(resolved.is_dir(), f"xctestrun product is missing: {resolved}")
        return resolved
    host = resolve(target.get("TestHostPath"))
    tests = resolve(target.get("TestBundlePath"), host)
    require(host == (products / "Debug-iphonesimulator/Runner.app").resolve(), "Unexpected XCTest host path")
    require(tests in {(host / "PlugIns/RunnerTests.xctest").resolve(),
                      (products / "Debug-iphonesimulator/RunnerTests.xctest").resolve()},
            "Unexpected RunnerTests bundle path")
    require(target.get("TestHostBundleIdentifier", "io.github.liveyum.iotools") == "io.github.liveyum.iotools",
            "Wrong xctestrun host identifier")
    records = {}
    for name, bundle, bundle_id, executable in (
            ("host", host, "io.github.liveyum.iotools", "Runner"),
            ("tests", tests, "io.github.liveyum.iotools.RunnerTests", "RunnerTests")):
        info = plistlib.loads((bundle / "Info.plist").read_bytes())
        require(info.get("CFBundleIdentifier") == bundle_id and info.get("CFBundleExecutable") == executable,
                f"Wrong {name} bundle identity")
        binary = bundle / executable
        require(binary.is_file(), f"Missing {name} executable")
        records[name] = {"bundle": str(bundle), "executable": str(binary), "sha256": digest(binary)}
    return records


def decode_result(value):
    """Decode xcresulttool's documented typed JSON objects without guessing fields."""
    if isinstance(value, dict):
        if "_value" in value:
            return value["_value"]
        if "_values" in value:
            return [decode_result(row) for row in value["_values"]]
        return {key: decode_result(row) for key, row in value.items() if key != "_type"}
    return value


def verify_cases(summary, tests):
    require(summary.get("passedTests") == 5 and summary.get("failedTests") == 0
            and summary.get("skippedTests") == 0, "Expected exactly five passed, zero failed/skipped tests")
    plans = tests.get("summaries", [])
    require(len(plans) == 1, "Expected one actual test-plan run")
    targets = plans[0].get("testableSummaries", [])
    require(len(targets) == 1 and targets[0].get("targetName") in ("RunnerTests", "RunnerTests.xctest"),
            "Unexpected executed XCTest target")
    cases = []
    def visit(node):
        if "subtests" in node:
            for child in node["subtests"]:
                visit(child)
        elif "testStatus" in node:
            identifier = node.get("identifier", "")
            match = re.fullmatch(r"RunnerTests/(test[A-Za-z0-9_]+)(?:\(\))?", identifier)
            require(match and node["testStatus"] == "Success", f"Unexpected or unsuccessful XCTest case: {identifier}")
            cases.append(match[1])
    for node in targets[0].get("tests", []):
        visit(node)
    require(len(cases) == 5 and set(cases) == EXPECTED_METHODS,
            "Actual XCTest identifiers do not match all five required host cases exactly once")
    return sorted(cases)


def remaining(deadline):
    seconds = deadline - time.monotonic()
    require(seconds > 0, "The shared 1200-second host-test gate budget expired")
    return seconds


def execute_once(command, log, deadline):
    with log.open("w") as output:
        output.write("Command: " + json.dumps(command) + "\n")
        output.flush()
        result = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT, timeout=remaining(deadline))
    require(result.returncode == 0, f"Host-test phase failed; see {log.name}")


def run_host_tests(root, evidence, udid, architecture, settings, launch_host, preflight=None):
    started = time.monotonic()
    deadline = started + 1200
    revision = os.environ.get("IOTOOLS_SHA", "")
    require(re.fullmatch(r"[0-9a-f]{40}", revision), "Expected exact host-test source SHA")
    require(architecture == "arm64" and os.environ.get("DEVELOPER_DIR") == PINNED_DEVELOPER,
            "Split host-test experiment requires the frozen installed Xcode 26.2 and arm64")
    selection = json.loads((evidence / "xcode-selection.json").read_text())
    require(selection.get("source_sha") == revision and selection.get("status") == "selected"
            and selection.get("developer_dir") == PINNED_DEVELOPER
            and selection.get("simulator_sdk") == "26.2"
            and selection.get("simulator", {}).get("udid", "").lower() == udid.lower(),
            "Host tests do not match the validated same-run toolchain/simulator")
    version = subprocess.check_output(["xcrun", "xcodebuild", "-version"], text=True, timeout=min(30, remaining(deadline))).strip()
    sdk = subprocess.check_output(["xcrun", "--sdk", "iphonesimulator", "--show-sdk-version"], text=True, timeout=min(30, remaining(deadline))).strip()
    require(version == "Xcode 26.2\nBuild version 17C52" and sdk == "26.2", "Frozen Xcode/SDK changed")
    head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True,
                                   timeout=min(30, remaining(deadline))).strip()
    require(head == revision, "Checkout HEAD does not match host-test source SHA")
    subprocess.run(["git", "-C", str(root), "diff", "--exit-code", "HEAD", "--", *COMMITTED_INPUTS],
                   check=True, capture_output=True, timeout=min(30, remaining(deadline)))
    untracked = subprocess.check_output(["git", "-C", str(root), "ls-files", "--others", "--exclude-standard",
                                        "--", *COMMITTED_INPUTS], text=True, timeout=min(30, remaining(deadline)))
    require(not untracked.strip(), "Untracked host-test source inputs are present")
    # Preserve the installed Apple manuals, not only a web mirror. Missing manual
    # files are diagnostic only; unsupported build actions still fail the gate.
    for section, name in (("man1", "xcodebuild.1"), ("man5", "xcodebuild.xctestrun.5")):
        manual = Path(PINNED_DEVELOPER) / "usr/share/man" / section / name
        if manual.is_file():
            (evidence / name).write_bytes(manual.read_bytes())
    work = root / "mobile/build/ios-host-tests"
    require(not work.exists(), "Refusing stale host-test build products")
    result = evidence / "host-tests.xcresult"
    build_result = evidence / "host-build.xcresult"
    require(not result.exists() and not build_result.exists(), "Refusing existing host-test result evidence")
    source = source_inventory(root)
    command = build_command(settings, work, build_result)
    record = {"source_sha": revision, "status": "building_for_testing", "developer_dir": PINNED_DEVELOPER,
              "xcode_version": version, "sdk": sdk, "udid": udid, "total_timeout_seconds": 1200,
              "build_command": command, "source_inventory": source, "checkout_head": head,
              "committed_inputs_verified": list(COMMITTED_INPUTS)}
    def save():
        (evidence / "host-test-phases.json").write_text(json.dumps(record, indent=2))
    save()
    try:
        if preflight is not None:
            record["preflight"] = preflight(command, deadline)
        record["phase"] = "build-for-testing"
        save()
        execute_once(command, evidence / "host-build.log", deadline)
        record["status"] = "validating_generated_test_products"
        save()
        products = work / "Build/Products"
        descriptors = sorted(products.glob("*.xctestrun"))
        require(len(descriptors) == 1, "Expected exactly one newly generated xctestrun")
        descriptor = descriptors[0]
        original = descriptor.read_bytes()
        # The evidence copy is for review; __TESTROOT__ must resolve at the build.
        (evidence / "host-generated.xctestrun").write_bytes(original)
        record["xctestrun"] = {"path": str(descriptor), "sha256": digest(descriptor),
                                "evidence_copy": "host-generated.xctestrun"}
        bundles = descriptor_products(descriptor, products)
        record["products"] = bundles
        require(source_inventory(root) == source, "Source/native inputs changed during test build")
        for name, bundle in bundles.items():
            binary = bundle["executable"]
            archs = subprocess.check_output(["xcrun", "lipo", "-archs", binary], text=True, timeout=remaining(deadline)).split()
            build = subprocess.check_output(["xcrun", "vtool", "-show-build", binary], text=True, timeout=remaining(deadline))
            (evidence / f"host-{name}-mach-o.txt").write_text(build)
            require(archs == ["arm64"] and re.search(r"^\s*platform\s+IOSSIMULATOR\s*$", build, re.M),
                    f"Wrong {name} test-product architecture/platform")
        symbols = set(subprocess.check_output(["xcrun", "nm", "-gUj", bundles["host"]["executable"]],
                                             text=True, timeout=remaining(deadline)).split())
        require({"_IotoolsNativeABIVersion", "_IotoolsNativeOpen", "_IotoolsNativeCommand",
                 "_IotoolsNativeLifecycle", "_IotoolsNativeFree"} <= symbols, "Actual test host lacks Go ABI exports")
        # Launch exactly the generated test host normally before XCTest injects
        # RunnerTests. The caller retains its original bounded runtime checks.
        record["phase"] = "normal-launch-generated-host"
        save()
        launch_host(Path(bundles["host"]["bundle"]), deadline)
        command = test_command(descriptor, udid, result)
        record.update(status="testing_without_building", phase="test-without-building", test_command=command)
        save()
        execute_once(command, evidence / "host-tests.log", deadline)
        require(descriptor.read_bytes() == original, "Generated xctestrun changed during test execution")
        require(source_inventory(root) == source, "Source/native inputs changed during host tests")
        require(all(digest(Path(row["executable"])) == row["sha256"] for row in bundles.values()),
                "Built host/test executable changed during test execution")
        summary = json.loads(subprocess.check_output(["xcrun", "xcresulttool", "get", "test-results", "summary",
            "--path", str(result)], text=True, timeout=remaining(deadline)))
        (evidence / "host-tests-summary.json").write_text(json.dumps(summary, indent=2))
        def legacy(identifier=None):
            command = ["xcrun", "xcresulttool", "get", "object", "--legacy", "--format", "json", "--path", str(result)]
            if identifier:
                command += ["--id", identifier]
            return decode_result(json.loads(subprocess.check_output(command, text=True, timeout=remaining(deadline))))
        invocation = legacy()
        actions = invocation.get("actions", [])
        require(len(actions) == 1 and actions[0].get("actionResult", {}).get("status") == "succeeded",
                "Expected one successful actual XCTest action")
        actual_device = actions[0].get("runDestination", {}).get("targetDeviceRecord", {}).get("identifier", "")
        require(actual_device.lower() == udid.lower(), "XCTest result used a different simulator UUID")
        identifier = actions[0]["actionResult"].get("testsRef", {}).get("id")
        require(identifier, "Actual XCTest result lacks case evidence")
        tests = legacy(identifier)
        (evidence / "host-tests-cases.json").write_text(json.dumps(tests, indent=2))
        cases = verify_cases(summary, tests)
        remaining(deadline)
        record.update(status="passed_all_five_host_tests", passed_identifiers=cases,
                      passed=5, failed=0, skipped=0, result_bundle=result.name)
        return record
    except Exception as error:
        record.update(status="failed", error=str(error))
        raise
    finally:
        record["elapsed_seconds"] = time.monotonic() - started
        save()
