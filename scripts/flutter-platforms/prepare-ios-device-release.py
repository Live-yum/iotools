#!/usr/bin/env python3
"""Build the independent unsigned device job without the test-only iOS plugin.

Flutter 3.35.7 includes iOS dev plugins even in Release (flutter/flutter#163874).
Temporarily remove only integration_test, use normal pinned Flutter generation,
and restore source manifests in finally. Generated device metadata stays available
for the subsequent package gate. This wrapper never runs simulator builds/tests.
"""

import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
REQUIRED_PLUGINS = {"file_selector_ios": "FileSelectorPlugin", "path_provider_foundation": "PathProviderPlugin"}
REMOVED = b"  integration_test:\n    sdk: flutter\n"


def require(value, message):
    if not value:
        raise RuntimeError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def device_manifest(original):
    """Fail closed on unfamiliar manifests; all retained bytes stay identical."""
    section = re.search(rb"(?m)^dev_dependencies:\n((?:[ \t].*\n|\n)*)", original)
    require(section and section[1].count(REMOVED) == 1, "Expected exactly one integration_test SDK dev dependency")
    require(original.count(b"  integration_test:") == 1, "integration_test must occur only in dev_dependencies")
    start = section.start(1) + section[1].index(REMOVED)
    return original[:start] + original[start + len(REMOVED):]


def production_closure(graph):
    packages = {row["name"]: row for row in graph["packages"]}
    root = packages[graph["root"]]
    require(root.get("kind") == "root" and "directDependencies" in root, "Unsupported pub dependency graph")
    pending = list(root["directDependencies"])
    result = {}
    while pending:
        name = pending.pop()
        if name in result:
            continue
        require(name in packages, f"Production dependency missing from pub graph: {name}")
        row = packages[name]
        dependencies = sorted(row["directDependencies"])
        result[name] = {"version": row["version"], "source": row["source"], "dependencies": dependencies}
        pending.extend(dependencies)
    require("integration_test" not in result, "integration_test is a production dependency; cannot remove it")
    return dict(sorted(result.items()))


def locked_packages(data):
    require(data.startswith(b"#") or data.startswith(b"packages:"), "Unexpected pub lockfile")
    section = re.search(rb"(?ms)^packages:\n(.*?)(?=^[^ #\s]|\Z)", data)
    require(section, "pub lockfile has no packages section")
    return {match[1].decode(): match[0] for match in re.finditer(
        rb"(?m)^  ([a-zA-Z0-9_]+):\n(?:    .*\n|\n)*", section[1])}


def verify_production(original_graph, staged_graph, original_lock, staged_lock):
    before, after = production_closure(original_graph), production_closure(staged_graph)
    require(before == after, "Production dependency closure, versions or sources changed")
    original, staged = locked_packages(original_lock), locked_packages(staged_lock)
    for name, row in before.items():
        require(name in original and original[name] == staged.get(name), f"Production lock entry changed: {name}")
        version = re.search(rb'(?m)^    version: "?([^"\n]+)"?$', original[name])
        require(version and version[1].decode() == row["version"], f"Original graph disagrees with locked version: {name}")
    require("integration_test" not in {row["name"] for row in staged_graph["packages"]}, "integration_test remains resolved")
    return before


def plugin_names(metadata):
    return {row["name"] for row in metadata["plugins"]["ios"]}


def verify_metadata(mobile, original_metadata):
    metadata = json.loads((mobile / ".flutter-plugins-dependencies").read_text())
    names = plugin_names(metadata)
    expected = plugin_names(original_metadata) - {"integration_test"}
    require(names == expected and REQUIRED_PLUGINS.keys() <= names,
            "Device plugin inventory changed beyond integration_test removal")
    require(all(not row.get("dev_dependency") for row in metadata["plugins"]["ios"]), "Device graph still contains an iOS dev plugin")
    config = json.loads((mobile / ".dart_tool/package_config.json").read_text())
    require("integration_test" not in {row["name"] for row in config["packages"]}, "integration_test remains in package_config")
    registrant = (mobile / "ios/Runner/GeneratedPluginRegistrant.m").read_text()
    require("IntegrationTest" not in registrant and "integration_test" not in registrant, "Generated device registrant retains test plugin")
    for plugin, class_name in REQUIRED_PLUGINS.items():
        require(plugin in registrant and f"[{class_name} registerWithRegistrar:" in registrant, f"Required plugin registration missing: {plugin}")
    return sorted(names)


def source_inventory(root):
    """Hash app/assets, Go source and the already-built native device archive."""
    files = [root / "go.mod", root / "go.sum"]
    for relative in ("mobile/lib", "mobile/assets", "mobile/ios/Runner/Assets.xcassets",
                     "cmd/iotools-native", "internal", "mobile/native/ios-device-arm64"):
        path = root / relative
        if path.is_dir():
            files.extend(file for file in path.rglob("*") if file.is_file())
    return {str(file.relative_to(root)): digest(file.read_bytes()) for file in sorted(set(files)) if file.is_file()}


def run_command(command, mobile, evidence, name, timeout):
    try:
        result = subprocess.run(command, cwd=mobile, text=True, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        for stream, data in (("stdout", error.stdout), ("stderr", error.stderr)):
            data = data or ""
            if isinstance(data, bytes):
                data = data.decode(errors="replace")
            (evidence / f"device-release-{name}.{stream}.txt").write_text(data)
        raise
    (evidence / f"device-release-{name}.stdout.txt").write_text(result.stdout)
    (evidence / f"device-release-{name}.stderr.txt").write_text(result.stderr)
    require(result.returncode == 0, f"Device release {name} failed ({result.returncode}); see captured logs")
    return result.stdout


def build_device_release(root, revision):
    require(re.fullmatch(r"[a-zA-Z0-9._-]+", revision), "IOTOOLS_SHA must identify the source revision")
    mobile = root / "mobile"
    evidence = root / "platform-evidence/ios"
    evidence.mkdir(parents=True, exist_ok=True)
    manifest, lock = mobile / "pubspec.yaml", mobile / "pubspec.lock"
    original_manifest, original_lock = manifest.read_bytes(), lock.read_bytes()
    staged_manifest = device_manifest(original_manifest)
    original_metadata = json.loads((mobile / ".flutter-plugins-dependencies").read_text())
    require("integration_test" in plugin_names(original_metadata), "Expected original test plugin metadata")
    sources = source_inventory(root)
    report = {"source_sha": revision, "status": "running", "removed_dev_dependency": "integration_test",
              "original_pubspec_sha256": digest(original_manifest), "staged_pubspec_sha256": digest(staged_manifest),
              "original_lock_sha256": digest(original_lock), "source_inventory": sources}
    def command(args, name, timeout=180):
        return run_command(["flutter", *args], mobile, evidence, name, timeout)
    try:
        original_graph = json.loads(command(["pub", "deps", "--json"], "original-deps"))
        require(manifest.read_bytes() == original_manifest and lock.read_bytes() == original_lock,
                "Inspecting original dependency graph changed source manifests")
        manifest.write_bytes(staged_manifest)
        command(["pub", "get", "--offline"], "pub-get", 300)
        staged_graph = json.loads(command(["pub", "deps", "--json"], "staged-deps"))
        report["production_dependencies"] = verify_production(original_graph, staged_graph, original_lock, lock.read_bytes())
        report["staged_lock_sha256"] = digest(lock.read_bytes())
        report["required_plugins"] = verify_metadata(mobile, original_metadata)
        require(manifest.read_bytes() == staged_manifest and source_inventory(root) == sources, "Source or assets changed during dependency preparation")
        command(["build", "ios", "--release", "--no-codesign", "--no-pub", f"--dart-define=IOTOOLS_SHA={revision}"], "build", 1800)
        report["required_plugins"] = verify_metadata(mobile, original_metadata)
        require(manifest.read_bytes() == staged_manifest and source_inventory(root) == sources, "Source or assets changed during device build")
        # Recheck the lock after Flutter's normal project/Pod generation too.
        verify_production(original_graph, staged_graph, original_lock, lock.read_bytes())
        report["status"] = "device_release_built_with_isolated_test_dependency"
    except BaseException as error:
        report.update(status="failed", error=str(error))
        raise
    finally:
        # Preserve the exact generated graph/registrant for the package verifier.
        try:
            for source, destination in [
                (manifest, "device-release-staged-pubspec.yaml"), (lock, "device-release-staged-pubspec.lock"),
                (mobile / ".flutter-plugins-dependencies", "device-release-flutter-plugins-dependencies.json"),
                (mobile / ".dart_tool/package_config.json", "device-release-package-config.json"),
                (mobile / "ios/Runner/GeneratedPluginRegistrant.m", "device-release-GeneratedPluginRegistrant.m"),
            ]:
                if source.is_file():
                    (evidence / destination).write_bytes(source.read_bytes())
        finally:
            try:
                manifest.write_bytes(original_manifest)
            finally:
                lock.write_bytes(original_lock)
        report["manifests_restored"] = manifest.read_bytes() == original_manifest and lock.read_bytes() == original_lock
        (evidence / "device-release-dependencies.json").write_text(json.dumps(report, indent=2) + "\n")
        require(report["manifests_restored"], "Failed to restore original source manifests")
    return report


if __name__ == "__main__":
    require(platform.system() == "Darwin", "Device Release compilation requires macOS")
    result = build_device_release(ROOT, os.environ.get("IOTOOLS_SHA", ""))
    print(json.dumps({"status": result["status"], "source_sha": result["source_sha"],
                      "production_packages": len(result["production_dependencies"]),
                      "required_plugins": result["required_plugins"], "manifests_restored": result["manifests_restored"]}))
