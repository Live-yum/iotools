"""Reviewed release version, package inventory and mandatory runtime gates."""
REPOSITORY = 'Live-yum/iotools'
VERSION = 'v0.3.2'
FLUTTER_REVISION = 'adc901062556672b4138e18a4dc62a4be8f4b3c2'
PLATFORMS = ('linux-amd64', 'linux-arm64', 'windows-amd64', 'macos-amd64', 'macos-arm64')
EXPECTED = {f'{kind}-{platform}': '.zip' for kind in ('tui', 'flutter', 'web') for platform in PLATFORMS}
EXPECTED.update({'android-arm64-v8a-aot-test-signed': '.apk', 'android-universal-arm64-x86_64-aot-test-signed': '.apk',
                 'ios-device-arm64-unsigned': '.zip', 'ios-simulator-arm64-debug-developer': '.zip'})
# v0.3.2 uses current-candidate acceptance, so no historical harness or release
# builder exclusions are needed. A parent must have the exact complete Git tree.
EXCLUDED_FILES = set()
HISTORY = [
    {'name': 'fast-security', 'workflow': '.github/workflows/fast-pr.yml', 'whole_run_success': True,
     'jobs': ['checks'], 'steps': ['Verify locked modules', 'Vet and unit tests', 'Go vulnerability scan', 'Release and workflow fail-closed contracts']},
    {'name': 'android', 'workflow': '.github/workflows/android.yml', 'jobs': ['apk'],
     'whole_run_success': True, 'steps': ['Real emulator UI, protocol, editing and lifecycle tests']},
    {'name': 'tui', 'workflow': '.github/workflows/ci.yml', 'whole_run_success': True,
     'jobs': ['Native ubuntu-24.04', 'Native ubuntu-24.04-arm', 'Native windows-latest'],
     'steps': ['Native unit, TUI and real loopback protocol tests', 'Binary smoke test']},
    {'name': 'http-history-windows-icons', 'workflow': '.github/workflows/history-regression.yml',
     'whole_run_success': True,
     'jobs': ['history (ubuntu-22.04, linux)', 'history (windows-2022, windows)'],
     'steps': ['Native ABI verification', 'Flutter static and widget contracts',
               'Generate and verify platform icon alpha', 'Real desktop HTTP history UI'],
     'job_steps': {'history (windows-2022, windows)': ['Verify embedded Windows icon transparency']}},
    {'name': 'flutter-platforms', 'workflow': '.github/workflows/flutter-platforms.yml',
     'whole_run_success': True,
     'jobs': ['native (windows-2022, windows, amd64)', 'native (macos-15, macos, arm64)',
              'native (macos-15-intel, macos, amd64)', 'native (ubuntu-22.04, linux, amd64, linux-x64)',
              'native (ubuntu-22.04-arm, linux, arm64, linux-arm64)', 'web-contract', 'ios-device', 'ios'],
     'steps': [], 'job_steps': {'ios': ['Required real iOS host XCTest acceptance'],
     'native (windows-2022, windows, amd64)': ['Verify Windows Release icon transparency', 'Exact packaged Windows Release GUI and JVM independence']}},
]
