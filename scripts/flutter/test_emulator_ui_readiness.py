#!/usr/bin/env python3
"""Hermetic foreground recovery tests; no emulator, input, or network needed."""
import json
from pathlib import Path
import tempfile
import unittest
from xml.sax.saxutils import escape

from emulator_ui_readiness import ReadinessError, anr_wait_target, ensure_ready


HEALTHY = b'<hierarchy><node package="io.github.liveyum.iotools" text="ready" /></hierarchy>'


def dialog(title="System UI isn't responding", package='android', **attrs):
    values = {'resource-id': 'android:id/aerr_wait', 'enabled': 'true',
              'clickable': 'true', 'visible-to-user': 'true', 'bounds': '[60,603][372,656]'} | attrs
    fields = ' '.join(f'{key}="{value}"' for key, value in values.items())
    return (f'<hierarchy><node package="{package}" resource-id="android:id/alertTitle" '
            f'text="{escape(title)}"/><node package="{package}" {fields}/></hierarchy>').encode()


class ReadinessTests(unittest.TestCase):
    def test_recorded_ci_system_dialog_matches_and_recovers(self):
        # Minimized from run 37111918446 AOT failure, keeping node attributes.
        raw = Path(__file__).with_name("testdata").joinpath("system-ui-anr-window.xml").read_bytes()
        self.assertEqual(anr_wait_target(raw), (216, 629))
        _, report, _ = self.run_case([raw, HEALTHY])
        self.assertEqual(report["status"], "ready")

    def run_case(self, states, *, fails=False, input_error=False):
        calls = []
        clock = [0]
        states = iter(states)
        last = [HEALTHY]
        def adb(*args):
            calls.append(args)
            if args[:2] == ('exec-out', 'cat'):
                last[0] = next(states, last[0])
                return last[0]
            if args[:3] == ('shell', 'input', 'tap') and input_error:
                raise TimeoutError('uncertain input')
            return b'diagnostic'
        def sleep(seconds):
            clock[0] += seconds
        with tempfile.TemporaryDirectory() as folder:
            evidence = Path(folder)
            if fails:
                with self.assertRaises((ReadinessError, TimeoutError)):
                    ensure_ready(adb, evidence, now=lambda: clock[0], sleep=sleep, timeout=6)
            else:
                ensure_ready(adb, evidence, now=lambda: clock[0], sleep=sleep, timeout=6)
            report = json.loads((evidence / 'report.json').read_text())
            files = {p.name for p in evidence.iterdir()}
        return calls, report, files

    def test_healthy_device_is_not_clicked(self):
        calls, report, files = self.run_case([HEALTHY])
        self.assertEqual(report['status'], 'ready')
        self.assertEqual(report['clean_samples'], 3)
        self.assertFalse(any(c[:2] == ('shell', 'input') for c in calls))

    def test_known_stale_system_dialog_is_waited_once_with_evidence(self):
        calls, report, files = self.run_case([dialog(), dialog(), HEALTHY])
        self.assertEqual(report['status'], 'ready')
        self.assertEqual([c for c in calls if c[:2] == ('shell', 'input')],
                         [('shell', 'input', 'tap', '216', '629')])
        self.assertEqual(report['system_ui_wait_clicks'], 1)
        self.assertTrue({'01-window.xml', 'before-recovery-screen.png',
                         'before-recovery-logcat.txt', 'ready-screen.png',
                         'ready-logcat.txt'}.issubset(files))

    def test_application_or_unknown_anr_is_never_dismissed(self):
        for title in ["iotools isn't responding", "Phone isn't responding", 'iotools keeps stopping']:
            with self.subTest(title=title):
                calls, report, files = self.run_case([dialog(title)], fails=True)
                self.assertEqual(report['status'], 'failed')
                self.assertFalse(any(c[:2] == ('shell', 'input') for c in calls))
                self.assertIn('failed-screen.png', files)

    def test_app_owned_imitation_is_not_a_system_dialog(self):
        self.assertIsNone(anr_wait_target(dialog(package='io.github.liveyum.iotools')))

    def test_recurring_anr_fails_without_second_input(self):
        calls, report, _ = self.run_case([dialog(), HEALTHY, dialog()], fails=True)
        self.assertIn('recurred', report['failure'])
        self.assertEqual(sum(c[:2] == ('shell', 'input') for c in calls), 1)

    def test_persistent_dialog_fails_with_no_timeout_extension(self):
        calls, report, _ = self.run_case([dialog()], fails=True)
        self.assertIn('setup budget', report['failure'])
        self.assertEqual(sum(c[:2] == ('shell', 'input') for c in calls), 1)

    def test_uncertain_input_is_never_retried(self):
        calls, report, _ = self.run_case([dialog()], fails=True, input_error=True)
        self.assertEqual(report['status'], 'failed')
        self.assertEqual(sum(c[:2] == ('shell', 'input') for c in calls), 1)

    def test_invisible_disabled_or_invalid_wait_control_is_rejected(self):
        for attrs in [{'enabled': 'false'}, {'clickable': 'false'}, {'visible-to-user': 'false'},
                      {'bounds': '[0,0][0,0]'}, {'bounds': 'unknown'}]:
            with self.subTest(attrs=attrs):
                calls, _, _ = self.run_case([dialog(**attrs)], fails=True)
                self.assertFalse(any(c[:2] == ('shell', 'input') for c in calls))

    def test_shell_dump_without_optional_visibility_attribute(self):
        raw = dialog().replace(b' visible-to-user="true"', b'')
        self.assertEqual(anr_wait_target(raw), (216, 629))
        calls, report, _ = self.run_case([raw, HEALTHY])
        self.assertEqual(report['status'], 'ready')

    def test_empty_dump_cannot_be_mistaken_for_healthy(self):
        for raw in [b'ERROR: could not get idle state', b'<hierarchy/>',
                    b'<hierarchy></hierarchy>']:
            with self.subTest(raw=raw), self.assertRaises(ReadinessError):
                anr_wait_target(raw)


if __name__ == '__main__':
    unittest.main()
