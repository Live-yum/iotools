#!/usr/bin/env python3
"""Require the deliberately failing Flutter fixture to retain its first error."""
import pathlib
import sys


def verify(text):
    for marker in (
        'IOTOOLS_TEST_FAILURE_PRIMARY',
        'IOTOOLS_INTENTIONAL_ORIGINAL_FAILURE',
        'IOTOOLS_ORIGINAL_STACK_MARKER',
        'IOTOOLS_DIAGNOSTIC_SERIALIZATION_FAILED',
        'Some tests failed',
    ):
        if marker not in text:
            raise ValueError(f'intentional failure evidence missing {marker}')
    if 'All tests passed' in text:
        raise ValueError('intentional failure was incorrectly reported as passed')


if __name__ == '__main__':
    verify(pathlib.Path(sys.argv[1]).read_text())
    print('Intentional Flutter failure retained original error and stack; remained failed.')
