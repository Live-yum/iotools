#!/usr/bin/env python3
"""One-shot comparison of immutable AOT bytes on disposable API29 emulators.

Never rebuild the app, retry startup, disable ProfileInstaller, change the test
timeout, or turn a diagnostic result into release approval. Downloads only use
the job's ordinary read-only GitHub token; signed redirects never receive it.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

REPOSITORY = "Live-yum/iotools"
SOURCE_SHA = "1465af9cbdf438bcca463066e3fd577ab440f04d"
SOURCE_RUN = 37082225597
PACKAGE = "io.github.liveyum.iotools"
APK_SHA = "72d45b9e6636cb93633d8f06fb0a9d25648a49787d7c77a7b54e590ce1bcc0e0"
TEST_SHA = "277d3aabf636c8d6685434d90d1099bda562f832957f26231d645d0ef3aaa273"
FINGERPRINT = "Android/sdk_phone_x86_64/generic_x86_64:10/QSR1.210820.001/7663313:userdebug/test-keys"
ARTIFACTS = (
    {"id": 11259883876, "name": f"iotools-flutter-aot-{SOURCE_SHA}",
     "archive_sha256": "d769e155bc4c531003bc90578f78833490debb2db2619833d9d3c5b7d3e3c94a",
     "member": f"iotools-flutter-universal-{SOURCE_SHA}-aot-test-signed.apk",
     "output": "app.apk", "sha256": APK_SHA},
    {"id": 11259864016, "name": f"iotools-flutter-evidence-{SOURCE_SHA}",
     "archive_sha256": "34cf6ecc002cab6930dea33c844fce0cc1fd7828d6849e99fb9aff33e0d75dd1",
     "member": "android-evidence/aot-test/instrumentation.apk",
     "output": "instrumentation.apk", "sha256": TEST_SHA},
)
# Preserve the original @Test(timeout=480_000) byte-for-byte. The original
# runner's outer adb deadline was 10 minutes; this is the same watchdog.
INSTRUMENTATION_WATCHDOG = 600
COLD_OBSERVATION_SECONDS = 30
PORTS = tuple(range(48410, 48417))
DIAGNOSTIC_REF = 'refs/heads/diag/aot-1465-once'


class DiagnosticError(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise DiagnosticError(message)


def check_launch_context(env, event):
    require(env.get('GITHUB_EVENT_NAME') == 'push' and
            env.get('GITHUB_REF') == DIAGNOSTIC_REF and
            env.get('GITHUB_RUN_ATTEMPT') == '1' and
            event.get('created') is True and event.get('deleted') is False and
            event.get('before') == '0' * 40 and event.get('ref') == DIAGNOSTIC_REF and
            re.fullmatch(r'[0-9a-f]{40}', env.get('GITHUB_SHA', '')) is not None and
            event.get('after') == env.get('GITHUB_SHA') == env.get('DIAGNOSTIC_WORKFLOW_SHA'),
            'Only the first creation of the dedicated diagnostic branch is allowed; no rerun')


def digest(path):
    with Path(path).open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def write_json(path, value):
    Path(path).write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def api_get(path, token):
    request = urllib.request.Request(
        f"https://api.github.com/repos/{REPOSITORY}/{path}",
        headers={"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json",
                 "X-GitHub-Api-Version": "2022-11-28"})
    return urllib.request.build_opener(NoRedirect()).open(request, timeout=60)


def check_metadata(metadata, spec):
    run = metadata.get("workflow_run", {})
    require(metadata.get("id") == spec["id"] and metadata.get("name") == spec["name"],
            "Artifact id/name differs from pinned evidence")
    require(not metadata.get("expired", True), "Pinned artifact expired; no fallback permitted")
    require(run.get("id") == SOURCE_RUN and run.get("head_sha") == SOURCE_SHA,
            "Artifact run/source identity differs from pinned evidence")
    if metadata.get("digest"):
        require(metadata["digest"] == "sha256:" + spec["archive_sha256"], "Artifact API digest mismatch")


def extract_verified(archive, spec, output):
    require(digest(archive) == spec["archive_sha256"], "Artifact archive SHA256 mismatch")
    with zipfile.ZipFile(archive) as zipped:
        entries = [x for x in zipped.infolist() if x.filename == spec["member"]]
        require(len(entries) == 1, "Expected exactly one pinned APK archive member")
        entry = entries[0]
        require(0 < entry.file_size <= 64 * 1024 * 1024, "APK member exceeds bounded size")
        data = zipped.read(entry)
    require(hashlib.sha256(data).hexdigest() == spec["sha256"], "APK SHA256 mismatch")
    # Select exactly one known member; never extract attacker-controlled paths.
    with (output / spec["output"]).open("xb") as target:
        target.write(data)


def prepare(inputs):
    inputs.mkdir(parents=True, exist_ok=False)
    identity = {"packaged_source_sha": SOURCE_SHA, "source_run": SOURCE_RUN,
                "repository": REPOSITORY, "status": "failed", "artifacts": []}
    try:
        token = os.environ.get("GH_TOKEN", "")
        require(bool(token), "Read-only GitHub job token is missing")
        for spec in ARTIFACTS:
            with api_get(f"actions/artifacts/{spec['id']}", token) as response:
                metadata = json.load(response)
            check_metadata(metadata, spec)
            try:
                with api_get(f"actions/artifacts/{spec['id']}/zip", token):
                    raise DiagnosticError("Artifact endpoint did not return its expected redirect")
            except urllib.error.HTTPError as redirect:
                require(redirect.code == 302, f"Artifact download HTTP status {redirect.code}; no retry")
                location = redirect.headers.get("Location", "")
            parsed = urllib.parse.urlparse(location)
            require(parsed.scheme == "https" and parsed.hostname and not parsed.username and
                    any(parsed.hostname.endswith("." + suffix) for suffix in
                        ("blob.core.windows.net", "githubusercontent.com", "amazonaws.com")),
                    "Unexpected artifact redirect destination")
            archive = inputs / f"{spec['id']}.zip"
            # No Authorization header on storage requests or their redirects.
            with urllib.request.urlopen(location, timeout=60) as response, archive.open("xb") as target:
                total = 0
                while chunk := response.read(1024 * 1024):
                    total += len(chunk)
                    require(total <= 128 * 1024 * 1024, "Artifact download exceeds size bound")
                    target.write(chunk)
            extract_verified(archive, spec, inputs)
            identity["artifacts"].append(dict(spec))
            archive.unlink()  # Private signed URLs/tokens are never saved.
        identity["status"] = "verified"
    except Exception as error:
        # Network exception strings may contain signed storage URLs. Do not log them.
        identity["error"] = str(error) if isinstance(error, DiagnosticError) else type(error).__name__
        raise DiagnosticError(identity["error"]) from None
    finally:
        write_json(inputs / "identity.json", identity)


class Commands:
    """Bound every subprocess; retain stdout, stderr and exit even on failure."""
    def __init__(self, output):
        self.output = output
        self.records = []
        self.serial = None

    def record(self, name, args, code, stdout, stderr, timeout):
        (self.output / name).write_bytes(stdout or b"")
        (self.output / (name + ".stderr")).write_bytes(stderr or b"")
        self.records.append({"file": name, "command": args, "exit": code, "timeout_seconds": timeout})
        write_json(self.output / "commands.json", self.records)
        return (stdout or b"").decode("utf-8", errors="replace"), code

    def run(self, name, args, timeout=10, required=True, env=None):
        try:
            result = subprocess.run(args, capture_output=True, timeout=timeout, env=env)
            stdout, stderr, code = result.stdout, result.stderr, result.returncode
        except subprocess.TimeoutExpired as error:
            stdout, stderr, code = error.stdout, error.stderr, 124
        except OSError as error:
            stdout, stderr, code = b"", str(error).encode(), 127
        text, code = self.record(name, args, code, stdout, stderr, timeout)
        require(not required or code == 0, f"{name} failed (exit {code})")
        return text, code

    def adb(self, name, *args, timeout=10, required=True):
        prefix = ["adb"] + (["-s", self.serial] if self.serial else [])
        return self.run(name, prefix + list(args), timeout, required)

    def instrument(self, snapshot):
        args = ["adb", "-s", self.serial, "shell", "am", "instrument", "-w", "-r",
                "-e", "class", PACKAGE + ".AotAcceptanceTest", "-e", "apk_sha256", APK_SHA,
                "-e", "build_sha", SOURCE_SHA, PACKAGE + ".test/androidx.test.runner.AndroidJUnitRunner"]
        name = "aot-instrumentation.txt"
        stdout_path, stderr_path = self.output / name, self.output / (name + ".stderr")
        started = time.monotonic()
        with stdout_path.open("wb") as stdout, stderr_path.open("wb") as stderr:
            process = subprocess.Popen(args, stdout=stdout, stderr=stderr)
            try:
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    pass
                snapshot("startup")  # One bounded observation, never another launch.
                try:
                    code = process.wait(timeout=max(0.1, INSTRUMENTATION_WATCHDOG - (time.monotonic() - started)))
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
                    code = 124
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=5)
        text, code = self.record(name, args, code, stdout_path.read_bytes(), stderr_path.read_bytes(),
                                 INSTRUMENTATION_WATCHDOG)
        require(code == 0, f"Instrumentation failed (exit {code})")
        require(re.search(r"^OK \(1 test\)\s*$", text, re.M) and
                not re.search(r"Process crashed|FAILURES!!!|INSTRUMENTATION_FAILED|shortMsg=", text),
                "Instrumentation did not report one passing original test")


def inspect_log(log):
    profile = re.findall(r"^\S+\s+\S+\s+(\d+)\s+\d+\s+D\s+ProfileInstaller\s*:\s*Installing profile for "
                         + re.escape(PACKAGE) + r"\s*$", log, re.M)
    starts = re.findall(r"Start proc (\d+):" + re.escape(PACKAGE) + r"/", log)
    app_pids = set(profile + starts)
    crashed = (f">>> {PACKAGE} <<<" in log or f"Process: {PACKAGE}," in log or
               any(re.search(r"Fatal signal.*pid " + re.escape(pid) + r"\b", log) for pid in app_pids))
    died = re.search(r"Process " + re.escape(PACKAGE) + r" \(pid \d+\) has died", log) is not None
    skipped = any("ProfileInstaller" in line and PACKAGE in line and
                  re.search(r"Skipping profile|already installed|RESULT_ALREADY_INSTALLED|skip file", line, re.I)
                  for line in log.splitlines())
    return {"profile_invoked": bool(profile), "profile_skipped": skipped, "profile_pids": sorted(set(profile)),
            "app_start_pids": sorted(set(starts)), "crash_detected": bool(crashed),
            "process_death_observed": died}


def verify_inputs(inputs):
    identity = json.loads((inputs / "identity.json").read_text())
    require(identity.get("status") == "verified" and identity.get("source_run") == SOURCE_RUN and
            identity.get("packaged_source_sha") == SOURCE_SHA and identity.get("repository") == REPOSITORY and
            identity.get("artifacts") == list(ARTIFACTS), "Input identity is not the pinned original artifacts")
    require(digest(inputs / "app.apk") == APK_SHA, "APK SHA256 mismatch before any device action")
    require(digest(inputs / "instrumentation.apk") == TEST_SHA, "Instrumentation SHA256 mismatch")
    return identity


def diagnose(arm, inputs, output, fixture, source, *, commands_class=Commands, sleep=time.sleep):
    # Exclusive directory is also a one-attempt guard. Never overwrite prior evidence.
    output.mkdir(parents=True, exist_ok=False)
    commands = commands_class(output)
    report = {"schema": 1, "arm": arm, "packaged_source_sha": SOURCE_SHA, "source_run": SOURCE_RUN,
              "diagnostic_workflow_sha": os.environ.get("DIAGNOSTIC_WORKFLOW_SHA"),
              "apk_sha256": APK_SHA, "instrumentation_sha256": TEST_SHA, "attempts": 0,
              "release_gate_passed": False, "acceptance_passed": False, "status": "failed",
              "scope": "Isolated synthetic-loopback diagnosis; not release or physical ARM64 acceptance"}
    installed = False
    forwarded = []
    fixture_process = None
    fixture_log = None
    cleanup_errors = []
    result = 1

    def snapshot(label):
        for suffix, args in (
            ("pid.txt", ("shell", "pidof", PACKAGE)),
            ("screen.png", ("exec-out", "screencap", "-p")),
            ("window.txt", ("shell", "dumpsys", "window", "windows")),
            ("logcat.txt", ("logcat", "-d", "-v", "threadtime")),
        ):
            commands.adb(f"{label}-{suffix}", *args, timeout=4, required=False)

    try:
        report["input_identity"] = verify_inputs(inputs)
        devices, _ = commands.adb("devices.txt", "devices")
        entries = re.findall(r"^(\S+)\s+(device|offline|unauthorized)\s*$", devices, re.M)
        require(len(entries) == 1 and entries[0][1] == "device" and
                re.fullmatch(r"emulator-\d+", entries[0][0]), "Require exactly one disposable emulator, never a real device")
        commands.serial = entries[0][0]
        for prop, expected in (("ro.boot.qemu", "1"), ("ro.build.version.sdk", "29"),
                               ("ro.product.cpu.abi", "x86_64"), ("ro.build.fingerprint", FINGERPRINT)):
            value, _ = commands.adb(prop + ".txt", "shell", "getprop", prop)
            require(value.strip() == expected, f"Unexpected emulator {prop}; comparison is inconclusive")
        packages, _ = commands.adb("preinstalled.txt", "shell", "pm", "list", "packages", PACKAGE)
        require(not any(line.strip() in ("package:" + PACKAGE, "package:" + PACKAGE + ".test")
                        for line in packages.splitlines()), "Refuse installed app/test state; use a new disposable emulator")
        pid, code = commands.adb("preexisting-pid.txt", "shell", "pidof", PACKAGE, required=False)
        require(code in (0, 1) and not pid.strip(), "Refuse warm app process or unverified device state")
        commands.adb("density.txt", "shell", "wm", "density", "160")
        # An install timeout may still leave an installed package. It is ours to
        # stop because absence was established above; never retry installation.
        installed = True
        commands.adb("install-app.txt", "install", str(inputs / "app.apk"), timeout=90)
        path, _ = commands.adb("installed-path.txt", "shell", "pm", "path", PACKAGE)
        paths = re.findall(r"^package:(/[^\r\n]+)\s*$", path, re.M)
        require(len(paths) == 1 and paths[0].endswith("/base.apk"), "Expected one installed base APK")
        remote_hash, _ = commands.adb("installed-sha256.txt", "shell", "sha256sum", paths[0])
        require(remote_hash.split() and remote_hash.split()[0] == APK_SHA, "Installed APK SHA256 mismatch")
        commands.adb("package.txt", "shell", "dumpsys", "package", PACKAGE)
        if arm == "standalone":
            commands.adb("install-test.txt", "install", str(inputs / "instrumentation.apk"), timeout=90)
            fixture_log = (output / "fixture.log").open("wb")
            ready = output / "fixture-ready.json"
            fixture_process = subprocess.Popen([str(fixture.resolve()), "-ready", str(ready.resolve())],
                                               stdout=fixture_log, stderr=subprocess.STDOUT)
            for _ in range(50):  # Same 10-second fixture-readiness bound as original runner.
                require(fixture_process.poll() is None, "Loopback fixture exited before ready")
                if ready.is_file() and ready.stat().st_size:
                    break
                sleep(0.2)
            require(ready.is_file() and ready.stat().st_size, "Loopback fixture readiness timed out")
            for port in PORTS:
                commands.adb(f"reverse-{port}.txt", "reverse", f"tcp:{port}", f"tcp:{port}")
                forwarded.append(port)
            report["attempts"] = 1
            commands.instrument(snapshot)
            (output / "aot-device").mkdir()
            commands.adb("pull-evidence.txt", "pull",
                         f"/sdcard/Android/data/{PACKAGE}/files/aot-evidence/.", str(output / "aot-device"), timeout=30)
            env = os.environ | {"IOTOOLS_SHA": SOURCE_SHA}
            commands.run("original-verifier.txt", [sys.executable, str(source / "scripts/flutter/verify-aot-device.py"),
                                                   str(inputs / "app.apk"), str(output)], timeout=10, env=env)
            report["original_verifier_passed"] = True
        else:
            require(arm == "cold-launch", "Unknown diagnostic arm")
            report["attempts"] = 1
            launch, _ = commands.adb("launch.txt", "shell", "am", "start", "-W", "-a", "android.intent.action.MAIN",
                                     "-c", "android.intent.category.LAUNCHER", "-n", PACKAGE + "/.MainActivity", timeout=30)
            require(re.search(r"^Status: ok\s*$", launch, re.M) is not None and
                    not re.search(r"Error:|Exception|Warning: Activity not started", launch), "Ordinary launch failed")
            snapshot("startup")
            sleep(COLD_OBSERVATION_SECONDS)
        result = 0
    except Exception as error:
        report["error"] = str(error)
    finally:
        if installed:
            # Before cleanup: preserve actual process fate, all ordinary crash logs,
            # screen, native trace and tombstone permission errors. No adb root/su.
            snapshot("finish")
            commands.adb("crash-buffer.txt", "logcat", "-d", "-b", "crash", timeout=4, required=False)
            commands.adb("tombstone-list.txt", "shell", "ls", "-l", "/data/tombstones", timeout=4, required=False)
            commands.adb("tombstones-pull.txt", "pull", "/data/tombstones", str(output / "tombstones"), timeout=8, required=False)
            commands.adb("surfaceflinger.txt", "shell", "dumpsys", "SurfaceFlinger", timeout=4, required=False)
            current_pid = (output / "finish-pid.txt").read_text().strip()
            if re.fullmatch(r"\d+", current_pid):
                commands.adb("threads.txt", "shell", "ps", "-T", "-p", current_pid, timeout=4, required=False)
                commands.adb("native-backtrace.txt", "shell", "debuggerd", "-b", current_pid, timeout=4, required=False)
            if arm == "standalone" and not (output / "aot-device").exists():
                (output / "aot-device").mkdir()
                commands.adb("failed-pull-evidence.txt", "pull",
                             f"/sdcard/Android/data/{PACKAGE}/files/aot-evidence/.", str(output / "aot-device"),
                             timeout=30, required=False)
            log = (output / "finish-logcat.txt").read_text(errors="replace")
            report.update(inspect_log(log))
            observation_codes = {row["file"]: row["exit"] for row in commands.records}
            # A truncated/failed log collection cannot prove absence of a crash.
            if observation_codes.get("finish-logcat.txt") != 0:
                report["observation_failure"] = "Final app log collection failed; diagnosis is inconclusive"
                result = result or 2
            if not report["profile_invoked"] or report["profile_skipped"]:
                report["profile_result"] = "inconclusive: ProfileInstaller invocation absent or installation skipped"
                result = result or 2
            else:
                report["profile_result"] = "invoked; this alone does not prove profile installation succeeded"
            # Android may end the target after successful instrumentation. That
            # ordinary completion is distinct from a fatal crash or cold-launch death.
            if report["crash_detected"] or (arm == "cold-launch" and report["process_death_observed"]) or len(report["app_start_pids"]) > 1:
                report["process_failure"] = "Crash, death or multiple app process starts observed"
                result = 1
            if arm == "cold-launch" and result == 0:
                startup_pid = (output / "startup-pid.txt").read_text().strip()
                window = (output / "finish-window.txt").read_text(errors="replace")
                visible = any(PACKAGE + "/" in line and "MainActivity" in line and
                              ("mCurrentFocus=" in line or "mFocusedApp=" in line) for line in window.splitlines())
                screens = [output / (label + "-screen.png") for label in ("startup", "finish")]
                essential = ("startup-pid.txt", "startup-screen.png", "finish-pid.txt", "finish-screen.png", "finish-window.txt")
                if not (current_pid == startup_pid and current_pid in report["profile_pids"] and visible and
                        all(observation_codes.get(name) == 0 for name in essential) and
                        all(p.read_bytes().startswith(b"\x89PNG\r\n\x1a\n") for p in screens)):
                    report["process_failure"] = "Cold launch lacks same live PID, focused activity or startup screenshots"
                    result = 1
            for target in (PACKAGE, PACKAGE + ".test"):
                _, code = commands.adb("cleanup-" + target + ".txt", "shell", "am", "force-stop", target, required=False)
                if code:
                    cleanup_errors.append(f"force-stop {target}: {code}")
            for port in forwarded:
                _, code = commands.adb(f"cleanup-reverse-{port}.txt", "reverse", "--remove", f"tcp:{port}", required=False)
                if code:
                    cleanup_errors.append(f"reverse {port}: {code}")
        if fixture_process is not None:
            try:
                fixture_process.terminate()
                try:
                    fixture_process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    fixture_process.kill()
                    fixture_process.wait(timeout=5)
                    cleanup_errors.append("Fixture required kill after termination deadline")
            except Exception as error:
                cleanup_errors.append("Fixture cleanup failed: " + type(error).__name__)
        if fixture_log is not None:
            fixture_log.close()
        if cleanup_errors:
            result = 1
        report["cleanup_errors"] = cleanup_errors
        report["cleanup_passed"] = not cleanup_errors
        report["acceptance_passed"] = arm == "standalone" and result == 0
        report["exit"] = result
        report["status"] = ("cold_launch_survived_not_acceptance" if arm == "cold-launch" else
                            "original_standalone_acceptance_passed_not_release_approval") if result == 0 else (
                                "inconclusive" if result == 2 else "failed")
        write_json(output / "result.json", report)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    download = sub.add_parser("prepare")
    download.add_argument("--inputs", type=Path, required=True)
    run = sub.add_parser("run")
    run.add_argument("--inputs", type=Path, required=True)
    run.add_argument("--output", type=Path, required=True)
    run.add_argument("--arm", choices=("cold-launch", "standalone"), required=True)
    run.add_argument("--fixture", type=Path, required=True)
    run.add_argument("--source", type=Path, required=True)
    args = parser.parse_args()
    def interrupted(signum, _frame):
        raise DiagnosticError(f"Diagnostic interrupted by signal {signum}; no retry")
    signal.signal(signal.SIGTERM, interrupted)
    try:
        check_launch_context(os.environ, json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text()))
        if args.command == "prepare":
            prepare(args.inputs)
            return 0
        return diagnose(args.arm, args.inputs, args.output, args.fixture, args.source)
    except Exception as error:
        print(f"AOT diagnostic failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
