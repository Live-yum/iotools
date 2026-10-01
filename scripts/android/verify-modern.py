#!/usr/bin/env python3
"""Ensure the Android product contains only the structured native application."""
from pathlib import Path
import re
import sys
import zipfile
root = Path(__file__).resolve().parents[2]
removed = ["internal/mobile", "internal/tui/mobile_lifecycle.go", "internal/tui/clipboard_android.go", "android/app/src/main/java/io/github/liveyum/iotools/TerminalCanvas.java"]
for name in removed:
    if (root / name).exists(): raise SystemExit(f"Removed mobile UI remains: {name}")
for directory in [root/"android/app/src/main", root/"cmd/iotools-android", root/"internal/mobileapi"]:
    for file in directory.rglob("*"):
        if file.suffix not in {".java", ".go", ".c", ".xml", ".js", ".html"}: continue
        text = file.read_text()
        if re.search(r"TerminalCanvas|StartCanvas|IotoolsFrame|IotoolsResize|IotoolsPaste|tcell|tview|internal/tui|android\.webkit|xterm|IOF1", text):
            raise SystemExit(f"Legacy mobile rendering dependency: {file.relative_to(root)}")
print("Android source isolation passed")

if len(sys.argv) == 2:
    with zipfile.ZipFile(sys.argv[1]) as archive:
        for name in archive.namelist():
            if name.startswith("assets/") and (name.endswith((".js", ".html")) or "/vendor/" in name or "/docs/" in name):
                raise SystemExit(f"Unapproved mobile asset: {name}")
            if name.endswith(".dex"):
                data = archive.read(name)
                for marker in [b"Lio/github/liveyum/iotools/TerminalCanvas;", b"Landroid/webkit/WebView;", b"StartCanvas", b"IotoolsFrame"]:
                    if marker in data: raise SystemExit(f"Removed renderer in APK: {marker!r}")
    print("APK native application isolation passed")
