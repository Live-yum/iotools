"""Disposable loopback receiver for exact installed Windows Release UI tests."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import json
import os
import sys
import threading

folder = Path(sys.argv[1])
folder.mkdir(parents=True, exist_ok=True)
received = []
lock = threading.Lock()

def write_json(name, value):
    temporary = folder / (name + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False), encoding='utf-8')
    os.replace(temporary, folder / name)

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        size = int(self.headers.get('Content-Length', '-1'))
        if self.path != '/echo' or not 0 <= size <= 1048576:
            self.send_error(400)
            return
        body = self.rfile.read(size).decode('utf-8')
        with lock:
            received.append(body)
            write_json('received.json', received)
        data = 'Windows Release真实响应😀'.encode('utf-8')
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain; charset=utf-8')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass

server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
write_json('received.json', received)
write_json('ready.json', {'port': server.server_port})
server.serve_forever()
