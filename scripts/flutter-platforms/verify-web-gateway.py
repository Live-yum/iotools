#!/usr/bin/env python3
"""Exercise a host-native packaged gateway and its embedded Flutter assets.

Usage: python3 scripts/flutter-platforms/verify-web-gateway.py /path/to/iotools-web
Uses only Python's standard library, temporary files and loopback HTTP fixtures.
This is executable/API smoke coverage, not a claim of browser rendering coverage.
"""

import argparse
import http.cookiejar
import http.server
import io
import json
import os
from pathlib import Path
import queue
import re
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class Target(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    received = queue.Queue()
    slow_started = threading.Event()
    slow_cancelled = threading.Event()

    def log_message(self, *_):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.received.put((self.command, self.path, body))
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        if self.path == "/slow":
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            self.slow_started.set()
            try:
                for _ in range(600):
                    self.wfile.write(b"1\r\nx\r\n")
                    self.wfile.flush()
                    time.sleep(0.02)
            except (OSError, ConnectionError):
                self.slow_cancelled.set()
            return
        body = '{"message":"中文😀","n":18446744073709551615}'.encode()
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class Gateway:
    def __init__(self, origin):
        self.origin = origin
        self.http = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )
        self.csrf = ""
        self.session = ""
        status, _, body = self.request("GET", "/api/bootstrap")
        require(status == 200, "gateway bootstrap failed")
        data = json.loads(body)["data"]
        self.csrf = data["csrf"]
        require(data["capabilities"]["native_protocols"] is True, "fake backend capability")
        require(data["capabilities"]["gateway_required"] is True, "missing gateway capability")

    def request(self, method, path, body=None, headers=None):
        base = {"Origin": self.origin, "X-Iotools-CSRF": self.csrf}
        if self.session:
            base["X-Iotools-Session"] = self.session
        base.update(headers or {})
        req = urllib.request.Request(self.origin + path, data=body, headers=base, method=method)
        try:
            response = self.http.open(req, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, response.headers, response.read()

    def post(self, path, value, ok=True):
        status, _, raw = self.request("POST", path, json.dumps(value, ensure_ascii=False).encode(),
                                      {"Content-Type": "application/json"})
        envelope = json.loads(raw)
        require(envelope.get("ok") is ok, f"{path}: expected ok={ok}, got {status} {envelope}")
        if ok:
            require(status == 200, f"{path}: {status}")
        return envelope

    def command(self, op, ok=True, **values):
        return self.post("/api/command", {"op": op, **values}, ok=ok).get("data")

    def open(self, path, **options):
        result = self.post("/api/open", {"path": path, **options})
        self.session = result["session"]
        require(result["data"]["path"] == path, "private root leaked or path changed")
        return result["data"]

    def upload(self, name, body, bundle=False, limit=1024*1024, ok=True):
        query = urllib.parse.urlencode({"name": name, "limit": limit, "bundle": int(bundle)})
        status, _, raw = self.request("POST", "/api/files/upload?" + query, body)
        result = json.loads(raw)
        require(result.get("ok") is ok, f"upload: {status} {result}")
        return result.get("data")

    def preview(self, request):
        return self.command("preview", request_id=request)["token"]

    def done(self, status="completed"):
        events = []
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            reply = self.command("events")
            events.extend(reply["events"])
            for event in reply["events"]:
                if event["kind"] == "done":
                    require(event["data"]["status"] == status, f"execution failed: {events}")
                    return events
            time.sleep(0.02)
        raise AssertionError("operation never reached done")

    def lifecycle(self, action):
        self.post("/api/lifecycle", {"action": action})


def run_checks(gateway, target):
    for asset in ("/", "/main.dart.js", "/flutter_bootstrap.js", "/assets/FontManifest.json"):
        status, headers, body = gateway.request("GET", asset)
        require(status == 200 and len(body) > 10, f"missing compiled asset {asset}")
        require(headers["X-Content-Type-Options"] == "nosniff", "nosniff missing")
        require("connect-src 'self'" in headers["Content-Security-Policy"], "same-origin CSP missing")
        require(headers.get("Access-Control-Allow-Origin") is None, "gateway enabled CORS")
        if asset == "/main.dart.js":
            require(len(body) > 100000, "not a compiled Flutter application")
    for headers in ({"Origin": "https://evil.invalid"}, {"Host": "evil.invalid"},
                    {"X-Iotools-CSRF": "wrong"}, {"Sec-Fetch-Site": "cross-site"}):
        status, _, _ = gateway.request("POST", "/api/platform", b'{"method":"settings.get"}', headers)
        require(status == 403, f"security boundary accepted {headers}")
    require(gateway.request("GET", "/private-file")[0] == 404, "unknown asset unexpectedly served")
    gateway.post("/api/open", {"path": "missing.json"}, ok=False)
    for path in ("../outside", "/etc/passwd", "C:/private", "a\\b", "CON.txt"):
        gateway.post("/api/platform", {"method": "files.read", "args": {"path": path}}, ok=False)

    endpoint = f"http://127.0.0.1:{target.server_address[1]}"
    payload = {"n": 18446744073709551615, "s": "18446744073709551615", "text": "中文😀"}
    collection = {"version": 1, "requests": [
        {"id": "read", "protocol": "http", "action": "GET", "endpoint": endpoint + "/read"},
        {"id": "write", "protocol": "http", "action": "POST", "endpoint": endpoint + "/write", "params": {"json": payload}},
        {"id": "slow", "protocol": "http", "action": "GET", "endpoint": endpoint + "/slow", "timeout": "30s"},
    ]}
    imported = gateway.upload("配置.json", json.dumps(collection, ensure_ascii=False).encode())
    state = gateway.open(imported["path"], readOnly=True, history=True)
    require(state["options"] == {"read_only": True, "history": True}, "open options lost")
    require(state["requests"][1]["params"]["json"] == payload, "editable JSON lost exact values")
    gateway.command("preview", request_id="write", ok=False)
    require(Target.received.empty(), "open or read-only preview performed protocol I/O")
    gateway.command("options.set", options={"read_only": False, "history": False})
    token = gateway.preview("write")
    gateway.command("options.set", options={"read_only": False, "history": False})
    gateway.command("run", token=token, confirmed=True, ok=False)
    token = gateway.preview("write")
    gateway.command("run", token=token, ok=False)
    require(Target.received.empty(), "unconfirmed write performed I/O")
    gateway.command("run", token=token, confirmed=True)
    events = gateway.done()
    method, path, raw = Target.received.get(timeout=2)
    require((method, path, json.loads(raw)) == ("POST", "/write", payload), "wire JSON values changed")
    require("中文😀" in json.dumps(events, ensure_ascii=False), "Unicode response absent")
    gateway.command("run", token=token, confirmed=True, ok=False)
    require(Target.received.empty(), "used preview replayed")
    gateway.command("run", token=gateway.preview("read"))
    gateway.done()
    require(Target.received.get(timeout=2)[:2] == ("GET", "/read"), "real GET missing")
    for action in ("cancel", "pause"):
        Target.slow_started.clear()
        Target.slow_cancelled.clear()
        gateway.command("run", token=gateway.preview("slow"))
        require(Target.slow_started.wait(3), "slow HTTP did not start")
        if action == "cancel":
            gateway.command("cancel")
        else:
            gateway.lifecycle("pause")
        gateway.done("cancelled")
        require(Target.slow_cancelled.wait(3), "protocol transport did not cancel")
        require(Target.received.get(timeout=2)[:2] == ("GET", "/slow"), "unexpected protocol request")
        if action == "pause":
            gateway.lifecycle("resume")
            require(gateway.command("state")["running"] is False, "resume replayed request")
        require(Target.received.empty(), "cancel or resume replayed request")

    data = bytes(range(256)) * 4
    info = gateway.upload("binary.bin", data)
    ticket = gateway.post("/api/files/download", {"path": info["path"], "name": "二进制.bin", "limit": len(data)})["data"]["url"]
    status, headers, received = gateway.request("GET", ticket)
    require(status == 200 and received == data, "binary download corrupted")
    require(headers["Content-Type"] == "application/octet-stream", "unsafe download MIME")
    require(gateway.request("GET", ticket)[0] == 404, "download ticket reused")
    gateway.post("/api/files/download", {"path": info["path"], "limit": 1}, ok=False)
    gateway.post("/api/platform", {"method": "files.read", "args": {"path": info["path"]}}, ok=False)
    gateway.upload("oversize.bin", data, limit=1, ok=False)
    ticket = gateway.post("/api/files/download", {"text": "导出😀", "name": "out.txt"})["data"]["url"]
    require(gateway.request("GET", ticket)[2].decode() == "导出😀", "text export changed")
    for bad in ("../escape.json", "CON.txt", "trailing./file.json"):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as archive:
            archive.writestr(bad, "bad")
        gateway.upload("bad.zip", buf.getvalue(), bundle=True, ok=False)
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as archive:
        archive.writestr("nested/配置.json", json.dumps(collection, ensure_ascii=False))
    imported = gateway.upload("valid.zip", buf.getvalue(), bundle=True)
    gateway.lifecycle("close")
    gateway.command("state", ok=False)
    gateway.open(imported["files"][0]["path"])
    for args in ({"theme": "light", "history": True}, {"theme": "dark", "readOnly": True}):
        gateway.post("/api/platform", {"method": "settings.save", "args": args})
    settings = gateway.post("/api/platform", {"method": "settings.get"})["data"]
    require(settings["theme"] == "dark" and settings["history"] is True and settings["readOnly"] is True,
            "settings update lost fields")
    files = gateway.post("/api/platform", {"method": "files.list"})["data"]
    require(not any(".iotools-" in f["path"] or ".source.zip" in f["path"] for f in files), "temporary/private metadata exposed")
    gateway.lifecycle("close")
    print("PASS: packaged Flutter assets, same-origin/CSRF, real HTTP/exact JSON, confirmation/read-only/options, cancellation/lifecycle, files/ZIP/downloads and settings")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("executable", type=Path)
    args = parser.parse_args()
    executable = args.executable.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="iotools-web-smoke-") as directory:
        log_path = Path(directory) / "gateway.log"
        target = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Target)
        target.daemon_threads = True
        threading.Thread(target=target.serve_forever, daemon=True).start()
        try:
            with log_path.open("wb") as log:
                proc = subprocess.Popen([str(executable), "--data", str(Path(directory) / "private"), "--port", "0"],
                                        stdout=log, stderr=subprocess.STDOUT)
                try:
                    origin = None
                    deadline = time.monotonic() + 15
                    while time.monotonic() < deadline:
                        content = log_path.read_text(encoding="utf-8", errors="replace")
                        match = re.search(r"http://127\.0\.0\.1:\d+", content)
                        if match:
                            origin = match.group()
                            break
                        require(proc.poll() is None, f"gateway exited during startup: {content}")
                        time.sleep(0.05)
                    require(origin, f"gateway did not become ready: {content}")
                    run_checks(Gateway(origin), target)
                finally:
                    if proc.poll() is None:
                        # Windows terminate is a hard stop. POSIX SIGTERM verifies
                        # the gateway's graceful signal shutdown path.
                        proc.terminate()
                        try:
                            proc.wait(timeout=8)
                        except subprocess.TimeoutExpired:
                            proc.kill()
                            proc.wait(timeout=3)
                            raise AssertionError("gateway failed to terminate")
                    if os.name != "nt":
                        require(proc.returncode == 0, f"gateway did not shut down cleanly: {proc.returncode}")
        finally:
            target.shutdown()
            target.server_close()


if __name__ == "__main__":
    main()
