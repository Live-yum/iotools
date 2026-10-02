"""Shared release I/O helpers; no publication at import time."""
import hashlib
import json
import subprocess
from pathlib import Path
ROOT = Path(__file__).resolve().parents[2]

def require(value, message):
    if not value:
        raise RuntimeError(message)


def git(*args, root=ROOT):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True, encoding="utf-8").strip()


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def write_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
