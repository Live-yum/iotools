#!/usr/bin/env python3
"""Recover one stale emulator System UI ANR before normal-entry acceptance.

Never handles the application under test's dialogs, changes system settings, or
retries business actions. A healthy foreground is a prerequisite, not a pass.
"""
import json
from pathlib import Path
import re
import subprocess
import sys
import time
import uuid
import xml.etree.ElementTree as ET


class ReadinessError(RuntimeError):
    pass


def anr_wait_target(raw):
    start = raw.find(b'<hierarchy')
    end = raw.find(b'</hierarchy>', start)
    if start < 0 or end < 0:
        raise ReadinessError('No complete foreground UI hierarchy')
    root = ET.fromstring(raw[start:end + len(b'</hierarchy>')])
    # Only Android-owned application-error controls identify an ANR/crash.
    nodes = list(root.iter('node'))
    if not nodes:
        raise ReadinessError('Empty foreground UI hierarchy')
    controls = [n for n in nodes
                if n.get('package') == 'android'
                and n.get('resource-id', '').startswith('android:id/aerr_')]
    if not controls:
        return None
    titles = [n.get('text') for n in root.iter('node')
              if n.get('package') == 'android'
              and n.get('resource-id') == 'android:id/alertTitle']
    waits = [n for n in controls if n.get('resource-id') == 'android:id/aerr_wait']
    if titles != ["System UI isn't responding"] or len(waits) != 1:
        raise ReadinessError('Unknown or application error dialog; no recovery attempted')
    wait = waits[0]
    # Android's shell uiautomator omits invisible descendants, and older
    # versions do not serialize visible-to-user. Honor it if present.
    if (any(wait.get(key) != 'true' for key in ('enabled', 'clickable'))
            or wait.get('visible-to-user', 'true') != 'true'):
        raise ReadinessError('System UI Wait control is not visible and actionable')
    match = re.fullmatch(r'\[(\d+),(\d+)\]\[(\d+),(\d+)\]', wait.get('bounds', ''))
    if not match:
        raise ReadinessError('Invalid System UI Wait bounds')
    left, top, right, bottom = map(int, match.groups())
    if right <= left or bottom <= top:
        raise ReadinessError('Empty System UI Wait bounds')
    return ((left + right) // 2, (top + bottom) // 2)


def ensure_ready(adb, evidence, *, now=time.monotonic, sleep=time.sleep, timeout=45):
    evidence.mkdir(parents=True, exist_ok=True)
    report = {'status': 'running', 'system_ui_wait_clicks': 0, 'clean_samples': 0}
    deadline = now() + timeout
    sample = 0
    remote_prefix = "/data/local/tmp/iotools-readiness-" + uuid.uuid4().hex
    recovered_dialog_gone = False

    def diagnostics(label):
        for name, args in [('screen.png', ('exec-out', 'screencap', '-p')),
                           ('logcat.txt', ('logcat', '-d'))]:
            try:
                (evidence / f'{label}-{name}').write_bytes(adb(*args))
            except Exception as error:
                (evidence / f'{label}-{name}.error').write_text(type(error).__name__)

    try:
        while now() < deadline:
            sample += 1
            remote = f'{remote_prefix}-{sample}.xml'
            try:
                adb('shell', 'uiautomator', 'dump', remote)
                raw = adb('exec-out', 'cat', remote)
            finally:
                adb('shell', 'rm', '-f', remote)
            (evidence / f'{sample:02}-window.xml').write_bytes(raw)
            target = anr_wait_target(raw)
            if target is not None:
                report['clean_samples'] = 0
                if recovered_dialog_gone:
                    raise ReadinessError('System UI ANR recurred after recovery')
                if report['system_ui_wait_clicks'] == 0:
                    diagnostics('before-recovery')
                    # Coordinates come only from the visible Android Wait node.
                    # Record intent before input: an uncertain input is never retried.
                    report['system_ui_wait_clicks'] = 1
                    adb('shell', 'input', 'tap', *(str(v) for v in target))
            else:
                recovered_dialog_gone = report['system_ui_wait_clicks'] == 1
                report['clean_samples'] += 1
                if report['clean_samples'] == 3:
                    diagnostics('ready')
                    report['status'] = 'ready'
                    return report
            sleep(1)
        raise ReadinessError('Emulator foreground did not become healthy within its setup budget')
    except Exception as error:
        report['status'] = 'failed'
        report['failure'] = str(error)
        diagnostics('failed')
        raise
    finally:
        (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')


def adb(*args):
    return subprocess.run(['adb', *args], check=True, capture_output=True, timeout=10).stdout


if __name__ == '__main__':
    ensure_ready(adb, Path(sys.argv[1]))
