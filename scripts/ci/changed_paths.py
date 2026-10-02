#!/usr/bin/env python3
"""Select ordinary-PR specialties; candidate labels override this at job level."""
import fnmatch
import os
import subprocess
import sys

PATTERNS = {
    'android': ('mobile/lib/**', 'mobile/test/**', 'mobile/integration_test/**',
                'mobile/android/**', 'mobile/pubspec.*', 'internal/mobileapi/**',
                'internal/engine/**', 'internal/config/**', 'internal/crypto/**',
                'cmd/iotools-android/**', 'scripts/android/**', 'scripts/flutter/**',
                'scripts/android-fixtures/**', 'go.mod', 'go.sum',
                '.github/workflows/android.yml', 'scripts/ci/**'),
    'history': ('mobile/lib/**', 'mobile/test/**', 'mobile/integration_test/**',
                'mobile/pubspec.*', 'mobile/windows/**', 'mobile/linux/**',
                'internal/engine/**', 'internal/mobileapi/**', 'internal/webhost/**',
                'cmd/iotools-native/**', 'go.mod', 'go.sum', 'scripts/flutter-platforms/**',
                '.github/workflows/history-regression.yml', 'scripts/ci/**'),
}

def relevant(suite, paths):
    return any(fnmatch.fnmatchcase(path, pattern) for path in paths for pattern in PATTERNS[suite])

def main():
    suite, base, head = sys.argv[1:]
    if not base:
        result = True
    else:
        paths = subprocess.check_output(['git', 'diff', '--no-renames', '--name-only', '-z', base, head]).decode('utf-8').split('\0')
        result = relevant(suite, paths)
    with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as out:
        out.write('relevant=' + str(result).lower() + '\n')

if __name__ == '__main__':
    main()
