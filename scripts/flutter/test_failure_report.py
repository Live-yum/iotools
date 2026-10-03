import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location(
    'failure_report', pathlib.Path(__file__).with_name('verify-failure-report.py'))
report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(report)


class FailureReportTest(unittest.TestCase):
    markers = [
        'IOTOOLS_TEST_FAILURE_PRIMARY',
        'IOTOOLS_INTENTIONAL_ORIGINAL_FAILURE',
        'IOTOOLS_ORIGINAL_STACK_MARKER',
        'IOTOOLS_DIAGNOSTIC_SERIALIZATION_FAILED',
        'Some tests failed',
    ]

    def test_complete_failure_is_evidence(self):
        report.verify('\n'.join(self.markers))

    def test_every_original_failure_marker_is_required(self):
        for missing in self.markers:
            with self.subTest(missing=missing), self.assertRaises(ValueError):
                report.verify('\n'.join(m for m in self.markers if m != missing))

    def test_pass_never_covers_failure(self):
        with self.assertRaises(ValueError):
            report.verify('\n'.join(self.markers + ['All tests passed']))
