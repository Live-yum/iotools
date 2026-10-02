#!/usr/bin/env python3
"""Exercise the built Go native engine with real loopback HTTP and SQLite.

All HTTP traffic is loopback and all persisted data is synthetic, in TemporaryDirectory.
"""
import base64
import ctypes as C
import http.server
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import unittest

LIBRARY = Path(os.environ["IOTOOLS_NATIVE_LIBRARY"]).resolve()
EXPECTED_VERSION = os.environ["IOTOOLS_SHA"]
lib = C.CDLL(str(LIBRARY))
ptr = C.c_void_p
lib.IotoolsNativeABIVersion.restype = C.c_uint32
lib.IotoolsNativeOpen.argtypes = [C.c_char_p, C.c_size_t, C.c_char_p, C.c_size_t, C.c_uint32, C.POINTER(ptr), C.POINTER(C.c_size_t)]
lib.IotoolsNativeOpen.restype = C.c_uint64
lib.IotoolsNativeCommand.argtypes = [C.c_uint64, C.c_char_p, C.c_size_t, C.POINTER(ptr), C.POINTER(C.c_size_t)]
lib.IotoolsNativeCommand.restype = C.c_int32
lib.IotoolsNativeLifecycle.argtypes = [C.c_uint64, C.c_int32]
lib.IotoolsNativeLifecycle.restype = C.c_int32
lib.IotoolsNativeFree.argtypes = [ptr]
lib.IotoolsNativeFree.restype = None


def unpack_reply(out, size):
    try:
        return json.loads(C.string_at(out.value, size.value))
    finally:
        if out.value:
            lib.IotoolsNativeFree(out)


class Session:
    def __init__(self, root, name="a.yaml", history=False, existing=False, read_only=False):
        self.root = Path(root)
        self.path = self.root / name
        if not existing:
            self.path.write_text(json.dumps({"version": 1, "requests": []}))
        p, r = os.fsencode(self.path), os.fsencode(self.root)
        out, size = ptr(), C.c_size_t()
        flags = (1 if read_only else 0) | (2 if history else 0) | (4 if existing else 0)
        self.id = lib.IotoolsNativeOpen(p, len(p), r, len(r), flags, C.byref(out), C.byref(size))
        reply = unpack_reply(out, size)
        assert self.id and reply["ok"], reply
        self.state = reply["data"]

    def raw(self, **command):
        data = json.dumps(command, ensure_ascii=False).encode()
        out, size = ptr(), C.c_size_t()
        status = lib.IotoolsNativeCommand(self.id, data, len(data), C.byref(out), C.byref(size))
        reply = unpack_reply(out, size)
        assert status == 0, (status, reply)
        return reply

    def command(self, **command):
        reply = self.raw(**command)
        assert reply["ok"], reply
        return reply["data"]

    def close(self):
        if self.id:
            assert lib.IotoolsNativeLifecycle(self.id, 2) == 0
            self.id = 0

    def start(self, request):
        preview = self.command(op="preview", request=request)
        return self.command(op="run", token=preview["token"], confirmed=True)

    def done(self, timeout=10):
        events = []
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            polled = self.command(op="events")
            events.extend(polled["events"])
            if any(e["kind"] == "done" for e in events) and not polled["running"]:
                return events
            time.sleep(0.005)
        raise AssertionError(("run timed out", events))

    def run(self, request):
        self.start(request)
        events = self.done()
        return next(e["data"] for e in events if e["kind"] == "done"), events

    def rows(self):
        return self.command(op="history.list")


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.hits.append(self.path)
        if self.path.startswith("/cancel"):
            self.server.entered.set()
            self.server.release.wait(3)
        code = int(self.path.split("/")[-1]) if self.path.startswith("/status/") else 200
        body = json.dumps({"ok": code == 200, "text": "历史 中文 😀", "token": "SYNTHETIC_RESPONSE_TOKEN"}, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Set-Cookie", "SYNTHETIC_RESPONSE_COOKIE")
        self.send_header("X-Fixture", "loopback-only")
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_HEAD(self):
        self.server.hits.append(self.path)
        self.send_response(200)
        self.send_header("X-Fixture", "empty-body")
        self.end_headers()

    def log_message(self, *_args):
        pass


class NativeHistoryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        cls.server.daemon_threads = True
        cls.server.hits = []
        cls.server.entered = threading.Event()
        cls.server.release = threading.Event()
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.url = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.release.set()
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="synthetic-history-")
        self.root = Path(self.tmp.name)
        self.sessions = []

    def tearDown(self):
        for session in self.sessions:
            session.close()
        self.tmp.cleanup()

    def session(self, **kwargs):
        s = Session(self.root, **kwargs)
        self.sessions.append(s)
        return s

    def request(self, path="/ok", **overrides):
        value = {"id": "fixture", "protocol": "http", "action": "GET", "endpoint": self.url + path, "timeout": "5s"}
        value.update(overrides)
        return value

    def count_sql(self):
        with sqlite3.connect(self.root / "history.sqlite") as db:
            return db.execute("SELECT count(*) FROM http_history").fetchone()[0]

    def test_disabled_then_enabled_persists_real_response(self):
        s = self.session()
        self.assertEqual(lib.IotoolsNativeABIVersion(), 1)
        self.assertEqual(s.command(op="history.status"), {"exists": False, "enabled": False})
        self.assertEqual(s.rows(), [])
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(s.rows(), [])
        self.assertFalse((self.root / "history.sqlite").exists())
        s.command(op="options.set", options={"history": True})
        done, events = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(len(s.rows()), 1)
        self.assertEqual(self.count_sql(), 1)
        row = s.rows()[0]
        self.assertEqual(row["status"], 200)
        self.assertTrue(row["created_at"])
        detail = s.command(op="history.get", history_id=row["id"])
        body = detail["body"].encode()
        self.assertEqual(base64.b64decode(detail["raw_body_base64"]), body)
        self.assertEqual(json.loads(body)["text"], "历史 中文 😀")
        self.assertIn("SYNTHETIC_RESPONSE_TOKEN", detail["body"])
        self.assertIn("SYNTHETIC_RESPONSE_COOKIE", detail["headers"])
        if os.name != "nt":
            self.assertEqual((self.root / "history.sqlite").stat().st_mode & 0o777, 0o600)
        response = next(e for e in events if e["kind"] == "response")
        self.assertEqual(base64.b64decode(response["data"]["raw_body_base64"]), body)

    def test_http_error_responses_persist(self):
        s = self.session(history=True)
        for status in [400, 404, 500]:
            with self.subTest(status=status):
                done, _ = s.run(self.request(f"/status/{status}", id=f"status-{status}"))
                self.assertEqual(done["status"], "failed")
                self.assertIn(f"HTTP {status}", done["error"])
                self.assertEqual(s.rows()[0]["status"], status)
        self.assertEqual(self.count_sql(), 3)

    def test_connection_failure_then_retry_no_phantom_history(self):
        s = self.session(history=True)
        # Bound but non-listening TCP socket is a deterministic refused local port.
        with socket.socket() as unavailable:
            unavailable.bind(("127.0.0.1", 0))
            endpoint = f"http://127.0.0.1:{unavailable.getsockname()[1]}"
            done, _ = s.run(self.request(endpoint=endpoint))
        self.assertEqual(done["status"], "failed")
        self.assertEqual(s.rows(), [])
        self.assertEqual(self.count_sql(), 0)
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 1)
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 2)

    def test_cancel_in_flight_then_retry(self):
        s = self.session(history=True)
        self.server.entered.clear()
        self.server.release.clear()
        run = s.start(self.request("/cancel"))
        self.assertTrue(self.server.entered.wait(2), "request never arrived at loopback server")
        s.command(op="cancel", run_id=run["run_id"])
        events = s.done()
        done = next(e["data"] for e in events if e["kind"] == "done")
        self.assertEqual(done["status"], "cancelled")
        self.assertEqual(self.count_sql(), 0)
        self.server.release.set()
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 1)

    def test_per_request_persist_false_and_disabled_keep_existing_rows(self):
        s = self.session(history=True)
        s.run(self.request())
        done, _ = s.run(self.request(params={"persist": False}))
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 1)
        s.command(op="options.set", options={"history": False})
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 1)
        self.assertEqual(s.command(op="history.status"), {"exists": True, "enabled": False})
        self.assertEqual(len(s.rows()), 1)

    def test_readonly_history_skips_database_then_records_future_only(self):
        s = self.session(history=True, read_only=True)
        done, events = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertFalse((self.root / "history.sqlite").exists(), "read-only GET created a history database")
        self.assertEqual(s.rows(), [])
        self.assertEqual(s.command(op="history.status"), {"enabled": True, "exists": False})
        self.assertEqual([e["data"] for e in events if e["kind"] == "history_status"],
                         [{"recorded": False, "reason": "read_only", "message": "只读保护，本次未记录"}])
        self.assertTrue(s.command(op="state")["options"]["history"])
        s.command(op="options.set", options={"history": True, "read_only": False})
        done, events = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 1, "skipped responses must never be backfilled")
        self.assertFalse(any(e["kind"] == "history_status" for e in events))

    def test_readonly_history_existing_rows_remain_readable_without_writes(self):
        s = self.session(history=True)
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        original = s.rows()
        before = (self.root / "history.sqlite").read_bytes()
        s.command(op="options.set", options={"history": True, "read_only": True})
        done, events = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(s.rows(), original, "read-only GET appended a history record")
        detail = s.command(op="history.get", history_id=original[0]["id"])
        self.assertEqual(json.loads(detail["body"])["text"], "历史 中文 😀")
        self.assertEqual((self.root / "history.sqlite").read_bytes(), before)
        self.assertEqual([e["data"] for e in events if e["kind"] == "history_status"],
                         [{"recorded": False, "reason": "read_only", "message": "只读保护，本次未记录"}])

    def test_readonly_does_not_override_explicit_history_off_or_persist_false(self):
        s = self.session()
        cases = [(False, False, {}), (False, True, {}),
                 (True, False, {"persist": False}), (True, True, {"persist": False})]
        for history, read_only, params in cases:
            with self.subTest(history=history, read_only=read_only, params=params):
                s.command(op="options.set", options={"history": history, "read_only": read_only})
                done, events = s.run(self.request(params=params))
                self.assertEqual(done["status"], "completed")
                self.assertEqual(s.rows(), [])
                self.assertFalse(any(e["kind"] == "history_status" for e in events))
                self.assertEqual(s.command(op="history.status")["enabled"], history)

    def test_session_and_process_restart_and_collection_isolation(self):
        a = self.session(history=True)
        a.run(self.request())
        original = a.rows()[0]
        a.close()
        a = self.session(history=True, existing=True)
        self.assertEqual(a.rows(), [original])
        b = self.session(name="b.yaml", history=True)
        self.assertEqual(b.rows(), [])
        denied = b.raw(op="history.get", history_id=original["id"])
        self.assertFalse(denied["ok"])
        b.run(self.request(id="other-collection"))
        self.assertEqual(len(a.rows()), 1)
        self.assertEqual(len(b.rows()), 1)
        self.assertEqual(self.count_sql(), 2)
        self.assertEqual(len(a.command(op="history.collections")), 2)
        a.close()
        b.close()
        proc = subprocess.run([sys.executable, str(Path(__file__)), "--restart-read", str(self.root)], capture_output=True, text=True, timeout=10)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        proof = json.loads(proc.stdout)
        self.assertEqual(proof, {"a": 1, "b": 1, "version": EXPECTED_VERSION})

    def test_history_save_failure_surfaces_after_response_and_retry_recovers(self):
        s = self.session(history=True)
        s.run(self.request())
        with sqlite3.connect(self.root / "history.sqlite") as db:
            db.execute("CREATE TRIGGER fixture_save_failure BEFORE INSERT ON http_history BEGIN SELECT RAISE(FAIL, 'synthetic-save-failure'); END")
        done, events = s.run(self.request())
        self.assertEqual(done["status"], "failed")
        self.assertIn("HTTP completed but history save failed", done["error"])
        self.assertIn("synthetic-save-failure", done["error"])
        self.assertTrue(any(e["kind"] == "response" and e["data"]["status"] == 200 for e in events))
        self.assertEqual(self.count_sql(), 1)
        with sqlite3.connect(self.root / "history.sqlite") as db:
            db.execute("DROP TRIGGER fixture_save_failure")
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "completed")
        self.assertEqual(self.count_sql(), 2)

    def test_invalid_history_path_stops_before_network(self):
        s = self.session(history=True)
        (self.root / "history.sqlite").mkdir()
        hits = len(self.server.hits)
        done, _ = s.run(self.request())
        self.assertEqual(done["status"], "failed")
        self.assertIn("history must be a regular file", done["error"])
        self.assertEqual(len(self.server.hits), hits)

    def test_empty_head_body_persists(self):
        s = self.session(history=True)
        done, _ = s.run(self.request(action="HEAD"))
        self.assertEqual(done["status"], "completed")
        row = s.rows()[0]
        self.assertEqual(row["body_bytes"], 0)
        self.assertEqual(row["method"], "HEAD")
        self.assertEqual(self.count_sql(), 1)

    def test_request_preview_masks_synthetic_credentials_without_io(self):
        s = self.session()
        hits = len(self.server.hits)
        secrets = ["SYNTHETIC_PASSWORD", "SYNTHETIC_BEARER", "SYNTHETIC_AUTH", "SYNTHETIC_COOKIE", "SYNTHETIC_KEY", "SYNTHETIC_QUERY"]
        preview = s.command(op="preview", request=self.request("/ok?access_token=" + secrets[5], params={
            "password": secrets[0], "bearer": secrets[1],
            "headers": {"Authorization": secrets[2], "Cookie": secrets[3]},
            "crypto": {"key": {"value": secrets[4]}}
        }))
        view = json.dumps(preview, ensure_ascii=False)
        for secret in secrets:
            self.assertNotIn(secret, view)
        self.assertIn("••••••", view)
        self.assertEqual(len(self.server.hits), hits)
        self.assertFalse((self.root / "history.sqlite").exists())


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--restart-read":
        root = Path(sys.argv[2])
        a = Session(root, "a.yaml", history=True, existing=True)
        b = Session(root, "b.yaml", history=True, existing=True)
        result = {"a": len(a.rows()), "b": len(b.rows()), "version": a.command(op="state")["version"]}
        a.close()
        b.close()
        print(json.dumps(result))
    else:
        unittest.main(verbosity=2)
