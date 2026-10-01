#!/usr/bin/env python3
"""Verify an actual iOS bundle; run simulator host tests, never enroll/sign devices.

Run after the matching build-native.sh and Flutter build. Simulator artifacts are
native-host architecture Debug apps. Device artifacts are unsigned arm64 builds,
not installable IPAs. Neither stage claims full protocol or real-device acceptance.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import shutil
import subprocess
import tempfile
import time
import zipfile

from ios_release_gate import verify_production_bundle

ROOT = Path(__file__).resolve().parents[2]
SYMBOLS = {
    "_IotoolsNativeABIVersion", "_IotoolsNativeOpen", "_IotoolsNativeCommand",
    "_IotoolsNativeLifecycle", "_IotoolsNativeFree",
}


def run(*args, timeout=120):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=timeout)


def simulator_operation(evidence, label, *args, timeout=120):
    """Preserve actual command output at the original timeout; never retry a gate."""
    try:
        output = run(*args, timeout=timeout)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        output = error.output or ''
        if isinstance(output, bytes):
            output = output.decode('utf-8', errors='replace')
        (evidence / f'{label}.txt').write_text(output + '\n' + str(error))
        raise
    (evidence / f'{label}.txt').write_text(output)
    return output


def simulator_failure_diagnostics(evidence):
    """Bounded read-only simulator diagnostics; their result cannot pass a failed gate."""
    udid = os.environ.get('IOTOOLS_IOS_SIMULATOR_UDID', '')
    if not re.fullmatch(r'[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}', udid):
        return
    commands = {
        'failure-devices': ['xcrun', 'simctl', 'list', 'devices', '--json'],
        'failure-services': ['xcrun', 'simctl', 'spawn', udid, 'launchctl', 'list'],
        'failure-install-log': ['xcrun', 'simctl', 'spawn', udid, 'log', 'show', '--last', '2m',
                                '--style', 'compact', '--predicate', 'process == "installd" OR process == "appstored"'],
    }
    for label, command in commands.items():
        try:
            simulator_operation(evidence, label, *command, timeout=15)
        except Exception as error:
            with (evidence / f'{label}.txt').open('a') as output:
                output.write('\nDiagnostic unavailable: ' + str(error))


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require(value, message):
    if not value:
        raise RuntimeError(message)


def append_notices(archive, licenses):
    manifest = licenses / 'Go/MANIFEST.json'
    require(manifest.is_file() and (licenses / 'Go/INDEX.txt').stat().st_size > 0,
            'Go notice index/manifest must be present')
    records = json.loads(manifest.read_text())['Licenses']
    require(bool(records), 'Go notice manifest must not be empty')
    require((licenses / 'iotools-LICENSE').is_file(), 'Project license is missing')
    for record in records:
        relative = Path(record['File'])
        require(not relative.is_absolute() and '..' not in relative.parts, 'Unsafe notice path')
        require(sha256(licenses / 'Go' / relative) == record['SHA256'], 'Go notice hash mismatch')
    with zipfile.ZipFile(archive) as bundled:
        original = {entry.filename: (entry.CRC, entry.file_size) for entry in bundled.infolist()}
        require(not any(name.startswith('licenses/') for name in original), 'Refusing duplicate ZIP-level notices')
    # Notices are siblings of Runner.app, never edits to the tested app bundle.
    with zipfile.ZipFile(archive, 'a', zipfile.ZIP_DEFLATED) as bundled:
        for file in sorted(licenses.rglob('*')):
            require(not file.is_symlink(), 'Notice staging must not contain symlinks')
            if file.is_file():
                bundled.write(file, 'licenses/' + file.relative_to(licenses).as_posix())
    with zipfile.ZipFile(archive) as bundled:
        require(all((bundled.getinfo(name).CRC, bundled.getinfo(name).file_size) == value
                    for name, value in original.items()), 'Tested app archive entries changed')
        for file in licenses.rglob('*'):
            if file.is_file():
                name = 'licenses/' + file.relative_to(licenses).as_posix()
                require(hashlib.sha256(bundled.read(name)).hexdigest() == sha256(file),
                        'Packaged notice bytes differ')
    return {'go_license_files': len(records), 'go_manifest_sha256': sha256(manifest),
            'project_license_sha256': sha256(licenses / 'iotools-LICENSE'),
            'location': 'licenses/ alongside unchanged Runner.app'}


def collect_notices(archive, architecture, out):
    go_arch = {'arm64': 'arm64', 'x86_64': 'amd64'}[architecture]
    with tempfile.TemporaryDirectory(prefix='ios-notices-', dir=out) as folder:
        licenses = Path(folder) / 'licenses'
        licenses.mkdir()
        shutil.copyfile(ROOT / 'LICENSE', licenses / 'iotools-LICENSE')
        subprocess.run(['go', 'run', './scripts/notices', '-goos', 'ios', '-goarch', go_arch,
                        '-cgo', '1', str(licenses / 'Go'), './cmd/iotools-native'],
                       cwd=ROOT, check=True, timeout=300)
        return append_notices(archive, licenses)


def verify_bundle(app, kind, evidence):
    require(app.is_dir(), f"Application missing: {app}")
    info = plistlib.loads((app / "Info.plist").read_bytes())
    exe = app / info["CFBundleExecutable"]
    require(exe.is_file() and exe.stat().st_size > 100000, "Runner executable is absent or implausibly small")
    architectures = run("xcrun", "lipo", "-archs", str(exe)).split()
    expected = "arm64" if kind == "device" else {"arm64": "arm64", "x86_64": "x86_64"}.get(platform.machine())
    require(expected and architectures == [expected], f"Expected one {expected} slice, found {architectures}")
    symbols = set(run("xcrun", "nm", "-gUj", str(exe)).split())
    require(SYMBOLS <= symbols, f"Process FFI symbols missing: {sorted(SYMBOLS - symbols)}")
    build = run("xcrun", "vtool", "-show-build", str(exe))
    (evidence / f"{kind}-mach-o-build.txt").write_text(build)
    sdk_platform = "IOSSIMULATOR" if kind == "simulator" else "IOS"
    require(re.search(rf"^\s*platform\s+{sdk_platform}\s*$", build, re.MULTILINE), f"Wrong Mach-O platform, expected {sdk_platform}")
    require(info.get("CFBundleIdentifier") == "io.github.liveyum.iotools", "Unexpected application bundle identifier")
    require(info.get("MinimumOSVersion") == "15.0", "App and native minimum iOS versions must agree at 15.0")
    signature = subprocess.run(["codesign", "-dv", "--verbose=4", str(app)], text=True, capture_output=True, timeout=30)
    (evidence / f"{kind}-signature.txt").write_text(signature.stdout + signature.stderr)
    if kind == "device":
        require(signature.returncode != 0 and "not signed at all" in signature.stderr, "Device build was expected to be unsigned")
        require(not (app / "embedded.mobileprovision").exists(), "Unsigned build unexpectedly contains a provisioning profile")
        require(not (app / "_CodeSignature").exists(), "Unsigned build unexpectedly contains signing metadata")
    return info, {
        "architectures": architectures,
        "mach_o_platform": sdk_platform,
        "minimum_ios": info["MinimumOSVersion"],
        "native_entrypoints": sorted(SYMBOLS),
        "runner_sha256": sha256(exe),
        "signature": "unsigned; requires user's own signing before device installation" if kind == "device" else "simulator development build; no Apple account used",
    }


def select_simulator(devices, sdk_version, required_udid=None):
    sdk = re.fullmatch(r"(\d+)\.(\d+)(?:\.(\d+))?", sdk_version)
    require(sdk, f"Unexpected selected iPhone simulator SDK version: {sdk_version}")
    sdk_release = tuple(int(part) for part in sdk.groups()[:2])
    if required_udid is not None:
        require(re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", required_udid),
                "Invalid pinned iPhone simulator UDID")
    choices = []
    for runtime, rows in devices.items():
        version = re.fullmatch(r"com\.apple\.CoreSimulator\.SimRuntime\.iOS-(\d+)-(\d+)(?:-(\d+))?", runtime)
        # simctl can expose runtimes from other installed Xcodes. Only use this
        # selected SDK's major/minor release, even if a newer runtime is booted.
        if not version or tuple(int(part) for part in version.groups()[:2]) != sdk_release:
            continue
        for device in rows:
            if device.get("isAvailable") and device["name"].startswith("iPhone"):
                if required_udid is not None and device.get("udid", "").lower() != required_udid.lower():
                    continue
                choices.append((runtime, device))
    if required_udid is not None:
        require(choices, f"Pinned iPhone simulator {required_udid} is unavailable or does not match selected SDK {sdk_version}")
    else:
        require(choices, f"No available iPhone simulator matching selected SDK {sdk_version}; install its runtime in the selected Xcode")
    choices.sort(key=lambda row: (row[1].get("state") == "Booted", row[0], row[1]["name"]), reverse=True)
    return choices[0]


def simulator_xcode_settings(architecture, test_target=False):
    require(architecture in ("arm64", "x86_64"), f"Unsupported simulator architecture: {architecture}")
    # Match Flutter's explicit simulator SDK selection. The project defaults to
    # iphoneos, and host-architecture substitutions must not change this bundle's
    # already-verified single architecture when XCTest rebuilds the host.
    target = (["-project", str(ROOT / "mobile/ios/Runner.xcodeproj"), "-target", "RunnerTests"]
              if test_target else ["-workspace", str(ROOT / "mobile/ios/Runner.xcworkspace"), "-scheme", "Runner"])
    return [
        "xcrun", "xcodebuild", *target,
        "-configuration", "Debug", "-sdk", "iphonesimulator",
        f"ARCHS={architecture}", "ONLY_ACTIVE_ARCH=YES",
        f"BUILD_DIR={ROOT / 'mobile/build/ios'}", "CODE_SIGNING_ALLOWED=NO", "CODE_SIGNING_REQUIRED=NO", "CODE_SIGN_IDENTITY=",
    ]


def simulator_test_command(udid, result, architecture):
    return simulator_xcode_settings(architecture) + [
        # Apple's iOS Simulator destination keys are platform/name/id/OS.
        # Architecture remains pinned in ARCHS, independently of device lookup.
        "test", "-destination", f"platform=iOS Simulator,id={udid}",
        "-parallel-testing-enabled", "NO", "-only-testing:RunnerTests", "-resultBundlePath", str(result),
    ]


def test_simulator(app, info, evidence, report, save, *, run_host_tests=True):
    sdk_version = run("xcrun", "--sdk", "iphonesimulator", "--show-sdk-version").strip()
    report["simulator_sdk"] = sdk_version
    save()
    devices = json.loads(run("xcrun", "simctl", "list", "devices", "available", "--json"))["devices"]
    (evidence / "simulator-devices.json").write_text(json.dumps(devices, indent=2))
    runtime, selected = select_simulator(devices, sdk_version, os.environ.get("IOTOOLS_IOS_SIMULATOR_UDID"))
    udid = selected["udid"]
    if selected["state"] != "Booted":
        simulator_operation(evidence, 'simulator-boot', "xcrun", "simctl", "boot", udid)
    simulator_operation(evidence, 'simulator-bootstatus', "xcrun", "simctl", "bootstatus", udid, "-b", timeout=300)
    simulator_operation(evidence, 'simulator-install', "xcrun", "simctl", "install", udid, str(app))
    # Terminate any prior run without uninstalling or erasing simulator contents.
    subprocess.run(["xcrun", "simctl", "terminate", udid, info["CFBundleIdentifier"]], capture_output=True, timeout=30)
    launch = simulator_operation(evidence, 'simulator-launch', "xcrun", "simctl", "launch", udid, info["CFBundleIdentifier"], timeout=60).strip()
    report["normal_launch"] = {"simulator": selected["name"], "udid": udid, "runtime": runtime, "launch": launch}
    save()
    try:
        time.sleep(5)
        services = run("xcrun", "simctl", "spawn", udid, "launchctl", "list")
        (evidence / "simulator-services.txt").write_text(services)
        require(any(info["CFBundleIdentifier"] in line and line.split()[0].isdigit() for line in services.splitlines()), "App exited after launch")
        run("xcrun", "simctl", "io", udid, "screenshot", str(evidence / "normal-launch.png"))
        report["normal_launch"]["still_running_after_seconds"] = 5
    finally:
        subprocess.run(["xcrun", "simctl", "terminate", udid, info["CFBundleIdentifier"]], capture_output=True, timeout=30)
    save()

    if not run_host_tests:
        report['remaining_gates'] = [
            'All five native host XCTest cases: separate required CI gate',
            'Flutter full protocol interaction and document-provider save/reimport',
            'Signed physical-iPhone and real-network/device acceptance',
        ]
        save()
        return

    # Host tests exercise dlsym of the real linked Go archive, open/command/free,
    # pause/resume/close, private-path/size/cancellation boundaries and actual
    # UIDocumentPicker presentation. They do not automate saving to a provider.
    result = evidence / "host-tests.xcresult"
    require(not result.exists(), f"Refusing to overwrite existing test evidence: {result}")
    architecture = report["architectures"][0]
    command = simulator_test_command(udid, result, architecture)
    log = evidence / "host-tests.log"
    with log.open("w") as output:
        completed = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT, timeout=1200)
    if completed.returncode != 0:
        # Preserve eligibility and resolved build settings on the actual runner;
        # a destination error alone otherwise hides SDK/architecture filtering.
        for action, name, test_target in [("-showdestinations", "xcode-destinations.log", False),
                                          ("-showBuildSettings", "xcode-build-settings.log", False),
                                          ("-showBuildSettings", "xcode-runner-tests-build-settings.log", True)]:
            with (evidence / name).open("w") as output:
                try:
                    diagnostic = subprocess.run(simulator_xcode_settings(architecture, test_target=test_target) + [action],
                                                stdout=output, stderr=subprocess.STDOUT, timeout=120)
                    output.write(f"\nDiagnostic exit code: {diagnostic.returncode}\n")
                except subprocess.TimeoutExpired:
                    output.write("\nDiagnostic timed out after 120 seconds\n")
    require(completed.returncode == 0, f"iOS host tests failed; see {log}")
    require(result.exists(), "Xcode did not produce a test result bundle")
    # Xcode 16+ summary verifies tests actually ran rather than trusting exit 0.
    summary = json.loads(run("xcrun", "xcresulttool", "get", "test-results", "summary", "--path", str(result)))
    (evidence / "host-tests-summary.json").write_text(json.dumps(summary, indent=2))
    require(summary.get("failedTests") == 0 and summary.get("passedTests", 0) >= 5, "Expected all five iOS host tests to run and pass")
    report["host_tests"] = {
        "passed": summary["passedTests"], "failed": summary["failedTests"],
        "scope": "Actual process Go C ABI and lifecycle; export private-path, size, cancellation; real system picker presentation",
        "result_bundle": result.name,
    }
    report["remaining_gates"] = ["User-selected document-provider save and reimport", "Flutter full protocol interaction on iOS", "Signed physical-iPhone and real-network/device acceptance"]
    save()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=["simulator", "simulator-build", "simulator-compiled", "device"])
    args = parser.parse_args()
    require(platform.system() == "Darwin", "This verifies actual Xcode products and can run only on macOS")
    revision = os.environ.get("IOTOOLS_SHA", "")
    require(re.fullmatch(r"[a-zA-Z0-9._-]+", revision), "IOTOOLS_SHA must identify the source revision")
    app = ROOT / "mobile/build/ios" / ("iphoneos" if args.kind == "device" else "iphonesimulator") / "Runner.app"
    out = ROOT / "platform-dist"
    evidence = ROOT / "platform-evidence/ios"
    out.mkdir(exist_ok=True)
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"source_sha": revision, "kind": args.kind, "status": "running"}
    def save():
        (evidence / f"{args.kind}.json").write_text(json.dumps(report, indent=2))
    try:
        bundle_kind = 'device' if args.kind == 'device' else 'simulator'
        info, details = verify_bundle(app, bundle_kind, evidence)
        report.update(details, bundle_id=info["CFBundleIdentifier"])
        save()
        if args.kind == 'simulator-compiled':
            report['bundle_type'] = 'Debug simulator test/developer bundle; integration_test plugin/channel is included'
            report['test_channel_exposure'] = ['plugins.flutter.io/integration_test: captureScreenshot/allTestsFinished']
            report['remaining_gates'] = ['Simulator installation and normal launch', 'All five required host XCTest cases',
                                       'User-selected document-provider save/reimport and full protocol UI acceptance']
        elif args.kind != "device":
            test_simulator(app, info, evidence, report, save,
                           run_host_tests=args.kind != 'simulator-build')
        else:
            dependency_evidence = evidence / 'device-release-dependencies.json'
            dependency_report = json.loads(dependency_evidence.read_text())
            require(dependency_report.get('source_sha') == revision
                    and dependency_report.get('status') == 'device_release_built_with_isolated_test_dependency'
                    and dependency_report.get('manifests_restored') is True,
                    'Device dependency isolation/restoration evidence is missing or failed')
            require(sha256(ROOT / 'mobile/pubspec.yaml') == dependency_report['original_pubspec_sha256']
                    and sha256(ROOT / 'mobile/pubspec.lock') == dependency_report['original_lock_sha256'],
                    'Original dependency manifests are not restored')
            report['dependency_isolation'] = {
                'evidence_sha256': sha256(dependency_evidence),
                'removed_dev_dependency': dependency_report['removed_dev_dependency'],
                'production_dependency_count': len(dependency_report['production_dependencies']),
                'production_closure_sha256': hashlib.sha256(json.dumps(
                    dependency_report['production_dependencies'], sort_keys=True).encode()).hexdigest(),
                'original_lock_sha256': dependency_report['original_lock_sha256'],
                'staged_lock_sha256': dependency_report['staged_lock_sha256'],
                'source_and_assets_unchanged': True,
            }
            report['production_isolation'] = verify_production_bundle(
                app, ROOT / 'mobile/ios/Runner/GeneratedPluginRegistrant.m',
                ROOT / 'mobile/.flutter-plugins-dependencies')
            report["remaining_gates"] = ["Signing with user's own Apple identity", "Physical-iPhone runtime and full-protocol acceptance"]
        archive = out / f"ios-{args.kind}-{revision}.zip"
        run("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", str(app), str(archive), timeout=300)
        report['licenses'] = collect_notices(archive, report['architectures'][0], out)
        report.update(status='compiled_only_no_runtime_acceptance' if args.kind == 'simulator-compiled' else
                      'compiled_and_normally_launched_only' if args.kind == 'simulator-build'
                      else "passed_for_stated_scope", bytes=archive.stat().st_size, sha256=sha256(archive))
        (out / f"ios-{args.kind}-{revision}-manifest.json").write_text(json.dumps(report, indent=2))
        scope = ("此包仅通过模拟器目标编译及 Mach-O/FFI 静态检查；不代表模拟器安装、普通启动或 XCTest 通过。\n"
                 "这是 Debug 测试/开发包，包含 integration_test 的 captureScreenshot/allTestsFinished 测试通道，不作为生产应用交付。\n"
                 if args.kind == 'simulator-compiled' else
                 "此包仅验证模拟器编译、Mach-O/FFI 静态符号及普通启动；独立必需的 XCTest 门禁未由此包宣称通过。\n"
                 if args.kind == 'simulator-build' else
                 "模拟器完整门禁覆盖普通启动、真实 Go ABI/生命周期、导出边界和系统选择器展示；不等于全部协议交互通过。\n"
                 if args.kind == 'simulator' else
                 "此包仅验证 arm64 设备目标编译、Mach-O/FFI 静态符号及确实未签名；未在 iPhone 运行。\n")
        (out / f"ios-{args.kind}-README.zh-CN.txt").write_text(
            f"iOS {args.kind} 构建，架构：{', '.join(report['architectures'])}\n"
            "设备版本未签名，不是可直接安装到手机的 IPA。模拟器版本仅用于相应架构的 macOS / Xcode。\n"
            + scope +
            ("设备包已隔离 integration_test 开发依赖，并检查实际插件注册、Mach-O 链接及测试通道字节。\n"
             if args.kind == 'device' else '') +
            "选择实际目标的文件保存/重新导入及签名真机仍需独立验收。完整验证范围见随附证据。\n"
            "ZIP 中 licenses/ 含项目及精确 iOS Go 依赖许可证；Flutter 通知随 Runner.app 的资源保留。\n")
    except Exception as error:
        report.update(status="failed", error=str(error))
        if args.kind in ('simulator', 'simulator-build'):
            simulator_failure_diagnostics(evidence)
        raise
    finally:
        save()
        print(json.dumps(report))


if __name__ == "__main__":
    main()
