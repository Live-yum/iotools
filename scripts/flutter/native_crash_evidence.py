#!/usr/bin/env python3
"""Read DropBox once; persist only allowlisted target-app native stack metadata.

Raw device output stays in memory. No retries, elevation, tombstone-file reads,
registers, memory, log messages, symbols, paths or arbitrary abort text are saved.
DropBox may retain only the crashed thread; this is not an all-thread guarantee.
"""
from pathlib import Path
import os
import re
import selectors
import subprocess
import sys
import time

TARGET = 'io.github.liveyum.iotools'
COMMAND = ('adb', 'shell', 'dumpsys', 'dropbox', '--print', 'data_app_native_crash')
READ_TIMEOUT = 4
MAX_OUTPUT_BYTES = 4 * 1024 * 1024
ENTRY = re.compile(r'^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) data_app_native_crash \((?:compressed )?text, [0-9]+ bytes\)$')
THREAD = re.compile(r'^pid: ([0-9]+), tid: ([0-9]+), name: (.*?)\s+>>> (.*?) <<<$')
LOG_PREFIX = re.compile(r'^[FWEIDV]/DEBUG\s*\(\s*[0-9]+\s*\):\s*')
FRAME = re.compile(r'^\s+#([0-9]{2,3}) pc ([0-9a-f]{8,16})\s+(\S+)(.*)$')
SIGNAL = re.compile(r'^signal ([0-9]{1,2}) \((SIGABRT|SIGBUS|SIGFPE|SIGILL|SIGSEGV|SIGSTKFLT|SIGSTOP|SIGSYS|SIGTRAP)\), code (-?[0-9]{1,3}) \(')
THREAD_NAME = re.compile(r'(?:[0-9]+\.(?:ui|raster|io)|io\.worker\.[0-9]+|DartWorker|DartWorker[0-9]+|RenderThread|Signal Catcher|FinalizerDaemon|FinalizerWatchdogDaemon|ReferenceQueueDaemon|HeapTaskDaemon|Jit thread pool|Binder:[0-9]+_[0-9]+|HwBinder:[0-9]+_[0-9]+|liveyum\.iotools|io\.github\.liveyum\.iotools)')
MODULE = re.compile(r'(?:lib[A-Za-z0-9_.+-]{1,80}\.so|base\.apk|app_process(?:32|64)?|linker(?:64)?|boot[A-Za-z0-9_.+-]*\.(?:oat|art))')
CAUSES = ('null pointer dereference', 'invalid chunk state', 'corrupted chunk header',
          'allocation type mismatch', 'invalid sized delete', 'misaligned pointer',
          'double free', 'use after free', 'stack corruption', 'stack overflow',
          'tag check fault')


def filter_entry(lines, timestamp, expected_pid):
    """Reject ambiguous identity; reconstruct metadata, never copy raw lines."""
    if [line[9:] for line in lines if line.startswith('Process: ')] != [TARGET]:
        return None
    cmdlines = [line[9:] for line in lines if line.startswith('Cmdline: ')]
    if cmdlines and cmdlines != [TARGET]:
        return None
    headers = [THREAD.fullmatch(line) for line in lines if line.startswith('pid: ')]
    if not headers or any(header is None for header in headers):
        return None
    pid = headers[0][1]
    if pid != expected_pid:
        return None
    if any(header[1] != pid or header[4] != TARGET for header in headers):
        return None
    reported_pids = [line[5:] for line in lines if line.startswith('PID: ')]
    if reported_pids and reported_pids != [pid]:
        return None
    result = [f'entry_time={timestamp}', f'process={TARGET}']
    in_thread = in_backtrace = False
    frames = 0
    for line in lines:
        header = THREAD.fullmatch(line)
        if header:
            name = header[3] if THREAD_NAME.fullmatch(header[3]) else 'unlisted'
            result.append(f'pid={header[1]} tid={header[2]} thread={name}')
            in_thread, in_backtrace = True, False
        elif in_thread and (signal := SIGNAL.match(line)):
            result.append(f'signal={signal[1]} name={signal[2]} code={signal[3]}')
        elif in_thread and line.startswith(('Cause: ', 'Abort message: ')):
            causes = [cause for cause in CAUSES if cause in line.lower()]
            result.append('cause=' + ','.join(causes or ['unlisted']))
        elif in_thread and line.strip() == 'backtrace:':
            in_backtrace = True
        elif in_backtrace and line.lstrip().startswith('NOTE: '):
            # Android may precede its frames with unwinder availability notes.
            # Neither those notes nor suggested access changes are persisted.
            continue
        elif in_backtrace and (frame := FRAME.fullmatch(line)):
            # APK install directories and symbol/abort strings can hold arbitrary
            # data. Keep a module basename and a strictly hexadecimal BuildId.
            module = frame[3].rsplit('/', 1)[-1].rsplit('!', 1)[-1]
            if not MODULE.fullmatch(module):
                module = 'unlisted'
            build_id = re.search(r'\(BuildId: ([0-9a-fA-F]{8,128})\)', frame[4])
            result.append(f'frame={frame[1]} pc={frame[2]} module={module}'
                          + (f' build_id={build_id[1]}' if build_id else ''))
            frames += 1
        elif line.strip():
            in_backtrace = False
    return '\n'.join(result) + '\n' if frames else None


def filtered_latest(raw, expected_pid):
    """Select only exact target entries matching this suite's crash-header PID."""
    latest = None
    timestamp = None
    entry = []
    # A DropBox delimiter ends an entry even when the next tag is not native.
    for line in [*raw.splitlines(), '=' * 40]:
        match = ENTRY.fullmatch(line)
        if match or re.fullmatch(r'={10,}', line):
            if timestamp:
                candidate = filter_entry(entry, timestamp, expected_pid)
                if candidate is not None:
                    latest = candidate
            timestamp = match[1] if match else None
            entry = []
        elif timestamp:
            entry.append(line)
    return latest


class OutputLimitExceeded(RuntimeError):
    pass


def read_bounded(command, *, timeout, limit):
    """Bound raw output in memory as well as time; never create a raw tempfile."""
    deadline = time.monotonic() + timeout
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL) as process:
        try:
            data = bytearray()
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0 or not selector.select(remaining):
                        raise subprocess.TimeoutExpired(command, timeout)
                    part = os.read(process.stdout.fileno(), min(65536, limit + 1 - len(data)))
                    if not part:
                        break
                    data.extend(part)
                    if len(data) > limit:
                        raise OutputLimitExceeded()
            result = process.wait(timeout=max(0, deadline - time.monotonic()))
            return subprocess.CompletedProcess(command, result, bytes(data))
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()


def suite_crash_pid(suite_log):
    pids = set()
    with suite_log.open() as log:
        for line in log:
            header = THREAD.fullmatch(LOG_PREFIX.sub('', line.rstrip('\r\n')))
            if header and header[4] == TARGET:
                pids.add(header[1])
    return next(iter(pids)) if len(pids) == 1 else None


def collect(evidence, suite_log, *, run=read_bounded):
    evidence.mkdir(parents=True, exist_ok=True)
    status = 'status=unavailable\nreason=collector_did_not_complete\n'
    status_path = evidence / 'status.txt'
    status_path.write_text(status)
    try:
        # Reusing a local evidence folder must not leave an old successful stack
        # beside a new unavailable status.
        (evidence / 'stack.txt').unlink(missing_ok=True)
        pid = suite_crash_pid(suite_log)
        if pid is None:
            status = 'status=unavailable\nreason=no_unique_suite_crash_pid\n'
            return
        result = run(COMMAND, timeout=READ_TIMEOUT, limit=MAX_OUTPUT_BYTES)
        if result.returncode:
            status = 'status=unavailable\nreason=adb_failed\n'
        else:
            filtered = filtered_latest(result.stdout.decode('utf-8', errors='replace'), pid)
            if filtered is None:
                status = 'status=unavailable\nreason=no_matching_target_stack\n'
            else:
                (evidence / 'stack.txt').write_text(filtered)
                status = ('status=captured\nsource=data_app_native_crash\n'
                          'correlation=suite_tombstone_pid\n'
                          'thread_coverage=not_guaranteed\n')
    except OutputLimitExceeded:
        status = 'status=unavailable\nreason=output_limit\n'
    except subprocess.TimeoutExpired:
        status = 'status=unavailable\nreason=adb_timeout\n'
    except OSError:
        status = 'status=unavailable\nreason=read_or_write_failed\n'
    finally:
        status_path.write_text(status)


if __name__ == '__main__':
    collect(Path(sys.argv[1]), Path(sys.argv[2]))
