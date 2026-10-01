#!/usr/bin/env python3
"""Exercise the actual desktop C ABI with only Python's standard library.

Usage: python3 cmd/iotools-native/testdata/check_abi.py /absolute/libiotools.so
The library may be a Windows DLL or macOS dylib. Only temporary local files and
a loopback HTTP fixture are used; no external services or devices are touched.
"""

import ctypes as c
import http.server
import json
from pathlib import Path
import sys
import tempfile
import threading
import time


class Bridge:
    def __init__(self, path):
        self.lib = c.CDLL(str(Path(path).resolve()))
        self.lib.IotoolsNativeABIVersion.argtypes = []
        self.lib.IotoolsNativeABIVersion.restype = c.c_uint32
        self.lib.IotoolsNativeOpen.argtypes = [
            c.c_void_p, c.c_size_t, c.c_void_p, c.c_size_t, c.c_uint32,
            c.POINTER(c.c_void_p), c.POINTER(c.c_size_t),
        ]
        self.lib.IotoolsNativeOpen.restype = c.c_uint64
        self.lib.IotoolsNativeCommand.argtypes = [
            c.c_uint64, c.c_void_p, c.c_size_t,
            c.POINTER(c.c_void_p), c.POINTER(c.c_size_t),
        ]
        self.lib.IotoolsNativeCommand.restype = c.c_int32
        self.lib.IotoolsNativeLifecycle.argtypes = [c.c_uint64, c.c_int32]
        self.lib.IotoolsNativeLifecycle.restype = c.c_int32
        self.lib.IotoolsNativeFree.argtypes = [c.c_void_p]
        self.lib.IotoolsNativeFree.restype = None
        assert self.lib.IotoolsNativeABIVersion() == 1
        assert not hasattr(self.lib, "IotoolsUSBExchange")
        assert not hasattr(self.lib, "Java_io_github_liveyum_iotools_NativeRuntime_openBytes")

    def decode(self, pointer, count):
        try:
            assert pointer.value and 0 < count.value <= 16 * 1024 * 1024
            raw = c.string_at(pointer, count.value)
            assert b"\x00" not in raw, "reply size includes a trailing NUL"
            result = json.loads(raw.decode("utf-8", errors="strict"))
            assert isinstance(result, dict) and isinstance(result.get("ok"), bool)
            return result
        finally:
            if pointer.value:
                self.lib.IotoolsNativeFree(pointer)

    def open(self, path, root, flags=0):
        path = str(path).encode("utf-8") if not isinstance(path, bytes) else path
        root = str(root).encode("utf-8") if not isinstance(root, bytes) else root
        out, size = c.c_void_p(), c.c_size_t()
        handle = self.lib.IotoolsNativeOpen(path, len(path), root, len(root), flags, c.byref(out), c.byref(size))
        return handle, self.decode(out, size)

    def command(self, handle, value):
        value = json.dumps(value, ensure_ascii=False).encode("utf-8") if not isinstance(value, bytes) else value
        out, size = c.c_void_p(), c.c_size_t()
        status = self.lib.IotoolsNativeCommand(handle, value, len(value), c.byref(out), c.byref(size))
        return status, self.decode(out, size)

    def ok(self, handle, value):
        status, reply = self.command(handle, value)
        assert status == 0 and reply["ok"], (status, reply)
        return reply.get("data")

    def lifecycle(self, handle, action):
        return self.lib.IotoolsNativeLifecycle(handle, action)


def check(bridge, root):
    root = root / "私有目录-😀"
    root.mkdir()
    missing = root / "不存在.yaml"
    handle, reply = bridge.open(missing, root, 4)
    assert handle == 0 and not reply["ok"] and not missing.exists()
    handle, reply = bridge.open(missing, root)
    assert handle and reply["ok"] and missing.is_file()
    assert Path(reply["data"]["path"]).resolve() == missing.resolve()
    assert bridge.lifecycle(handle, 2) == 0

    for path, private_root, flags in [
        (b"", root, 0), (missing, b"", 0), (b"relative.yaml", root, 0),
        (missing, b"relative", 0), (str(missing).encode() + b"\x00ignored", root, 0),
        (str(missing).encode() + b"\xff", root, 0), (missing, root, 8),
        (root.parent / "outside.yaml", root, 0),
    ]:
        rejected, reply = bridge.open(path, private_root, flags)
        assert rejected == 0 and not reply["ok"], reply

    # Bounds must be checked before reading memory. These pointers contain only
    # one byte while the supplied lengths are deliberately enormous.
    out, size = c.c_void_p(), c.c_size_t()
    status = bridge.lib.IotoolsNativeCommand(0, b"x", c.c_size_t(-1).value, c.byref(out), c.byref(size))
    assert status == 1 and not bridge.decode(out, size)["ok"]
    rejected = bridge.lib.IotoolsNativeOpen(b"x", 16385, b"x", 1, 0, c.byref(out), c.byref(size))
    assert rejected == 0 and not bridge.decode(out, size)["ok"]
    status = bridge.lib.IotoolsNativeCommand(0, None, 1, c.byref(out), c.byref(size))
    assert status == 1 and not bridge.decode(out, size)["ok"]
    size.value = 999
    assert bridge.lib.IotoolsNativeOpen(None, 1, None, 1, 0, None, c.byref(size)) == 0
    assert size.value == 0
    out.value = 123
    assert bridge.lib.IotoolsNativeCommand(0, b"{}", 2, c.byref(out), None) == 1
    assert out.value is None
    bridge.lib.IotoolsNativeFree(None)

    handle, reply = bridge.open(missing, root, 4)
    assert handle and reply["ok"]
    for malformed in [b"\xff", b"", b"x" * (8 * 1024 * 1024 + 1)]:
        status, reply = bridge.command(handle, malformed)
        assert status == 1 and not reply["ok"]
    status, reply = bridge.command(handle, b"invalid-json")
    assert status == 0 and not reply["ok"]
    # UTF-8 byte lengths and exact n consumption, including no trailing NUL.
    payload = b'{"op":"state"}'
    extended = payload + b"invalid trailing bytes"
    status = bridge.lib.IotoolsNativeCommand(handle, extended, len(payload), c.byref(out), c.byref(size))
    assert status == 0 and bridge.decode(out, size)["ok"]
    assert bridge.lifecycle(handle, 9) == 1
    assert bridge.lifecycle(handle, 0) == 0
    assert bridge.ok(handle, {"op": "state"})["paused"] is True
    assert bridge.lifecycle(handle, 1) == 0
    assert bridge.ok(handle, {"op": "state"})["paused"] is False
    assert bridge.lifecycle(handle, 2) == 0
    assert bridge.lifecycle(handle, 2) == 2
    status, reply = bridge.command(handle, {"op": "state"})
    assert status == 2 and not reply["ok"]

    handles = []
    try:
        for _ in range(16):
            fresh, reply = bridge.open(missing, root, 4)
            assert fresh and fresh != handle and fresh not in handles and reply["ok"]
            handles.append(fresh)
        rejected, reply = bridge.open(missing, root, 4)
        assert rejected == 0 and not reply["ok"]
        assert bridge.lifecycle(handle, 2) == 2
        assert bridge.ok(handles[-1], {"op": "state"})["closed"] is False
    finally:
        for fresh in handles:
            assert bridge.lifecycle(fresh, 2) == 0

    calls = []

    class Fixture(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            self.rfile.read(int(self.headers.get("Content-Length", "0")))
            calls.append(self.path)
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write('{"message":"真实-😀"}'.encode())

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    endpoint = f"http://127.0.0.1:{server.server_port}/native"
    config = root / "本机验证.yaml"
    config.write_text(json.dumps({"version": 1, "requests": [{
        "id": "write", "name": "真实写入-😀", "protocol": "http",
        "action": "POST", "endpoint": endpoint, "params": {"body": "hello"},
    }]}, ensure_ascii=False), encoding="utf-8")
    handle = 0
    try:
        handle, reply = bridge.open(config, root, 4)
        assert handle and reply["ok"]
        assert reply["data"]["requests"][0]["name"] == "真实写入-😀"
        bridge.ok(handle, {"op": "catalog"})
        preview = bridge.ok(handle, {"op": "preview", "request_id": "write"})
        assert not calls, "startup or preview performed network I/O"
        status, reply = bridge.command(handle, {"op": "run", "token": preview["token"]})
        assert status == 0 and not reply["ok"] and not calls
        bridge.ok(handle, {"op": "run", "token": preview["token"], "confirmed": True})
        deadline = time.monotonic() + 10
        events = []
        while time.monotonic() < deadline:
            batch = bridge.ok(handle, {"op": "events"})
            events.extend(batch["events"])
            if any(event["kind"] == "done" for event in events):
                break
            time.sleep(0.01)
        else:
            raise AssertionError("explicit native request did not finish")
        done = next(event for event in events if event["kind"] == "done")
        assert done["data"]["status"] == "completed", events
        assert calls == ["/native"]
        status, reply = bridge.command(handle, {"op": "run", "token": preview["token"], "confirmed": True})
        assert status == 0 and not reply["ok"]
        assert bridge.lifecycle(handle, 0) == 0
        assert bridge.lifecycle(handle, 1) == 0
        bridge.ok(handle, {"op": "state"})
        assert calls == ["/native"], "resume replayed a request"
    finally:
        if handle:
            bridge.lifecycle(handle, 2)
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    bridge = Bridge(sys.argv[1])
    with tempfile.TemporaryDirectory(prefix="iotools-native-") as directory:
        check(bridge, Path(directory))
    print("PASS native ABI v1: UTF-8, lengths, bounds, ownership, roots, recovery, sessions, lifecycle, real HTTP, write confirmation")
