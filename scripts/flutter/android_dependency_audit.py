#!/usr/bin/env python3
"""Fail closed if the controlled Android path-provider isolation drifts."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import zipfile

ROOT = Path(__file__).resolve().parents[2]
BASELINE = Path(__file__).with_name('android_dependency_baseline.json')
JNI = {'jni', 'jni_flutter', 'jni_util'}

def require(condition, message):
    if not condition:
        raise ValueError(message)

def lock_versions(text):
    # pub's generated lockfile uses exactly these indentation levels. Reject
    # incomplete parsing rather than silently accepting an unknown format.
    packages = text.split('packages:\n', 1)[1].split('\nsdks:', 1)[0] + '\n'
    blocks = re.findall(r'^  ([a-zA-Z0-9_]+):\n((?:    .*\n|\n)+)', packages, re.M)
    result = {}
    for name, body in blocks:
        version = re.findall(r'^    version: "([^"\n]+)"$', body, re.M)
        require(len(version) == 1 and name not in result, f'Invalid lock entry: {name}')
        result[name] = version[0]
    require(len(result) == len(re.findall(r'^  \w+:$', packages, re.M)), 'Unparsed lock entries')
    require(bool(result), 'Empty lockfile')
    return result

def audit_lock(text, baseline):
    current = lock_versions(text)
    previous = baseline['lock_versions']
    require(current.get('path_provider_android') == '2.2.23', 'Expected path_provider_android 2.2.23')
    require(not (JNI & current.keys()), 'Unused Dart JNI dependency remains')
    blocks = dict(re.findall(r'^  (\w+):\n((?:    .*\n|\n)+)', text, re.M))
    if 'lock_entry_sha256' in baseline:
        for name, body in blocks.items():
            if name != 'path_provider_android':
                require(hashlib.sha256(body.encode()).hexdigest() == baseline['lock_entry_sha256'].get(name),
                        f'Unrelated lock entry changed: {name}')
        provider = blocks['path_provider_android']
        require('    dependency: "direct main"\n' in provider and '    source: hosted\n' in provider
                and '      url: "https://pub.dev"\n' in provider
                and re.search(r'      sha256: "?' + baseline['path_provider_android_archive_sha256'] + r'"?\n', provider),
                'Expected exact official hosted path-provider archive')
    removed = set(previous) - current.keys()
    require(removed == set(baseline['allowed_removed']), f'Unexpected removed closure: {sorted(removed)}')
    require(not (current.keys() - previous.keys()), 'Unexpected added dependency')
    drift = {name: (previous[name], version) for name, version in current.items()
             if name != 'path_provider_android' and previous[name] != version}
    require(not drift, f'Unrelated dependency versions changed: {drift}')
    return {'package_count': len(current), 'removed': sorted(removed), 'versions': current}

def audit_graph(graph, versions):
    entries = graph['packages']
    names = [entry['name'] for entry in entries]
    require(len(names) == len(set(names)), 'Duplicate dependency graph entries')
    require(not (JNI & set(names)), 'JNI remains in resolver graph')
    resolved = {entry['name']: entry['version'] for entry in entries if entry['kind'] != 'root'}
    require(resolved == versions, 'Resolver graph does not match lockfile')
    provider = next(entry for entry in entries if entry['name'] == 'path_provider_android')
    require(set(provider['dependencies']) == {'flutter', 'path_provider_platform_interface'},
            'Unexpected Android path-provider dependencies')

def audit_plugins(data):
    android = data['plugins']['android']
    names = {entry['name'] for entry in android}
    require(names == {'file_selector_android', 'path_provider_android', 'integration_test'},
            f'Unexpected Android plugin set: {sorted(names)}')
    require(not (JNI & {entry['name'] for entry in data['dependencyGraph']}), 'JNI plugin graph remains')
    require(next(entry for entry in android if entry['name'] == 'path_provider_android')['native_build'],
            'Expected official native path-provider plugin')
    return sorted(names)

def maven_coordinates(text):
    require('releaseRuntimeClasspath - ' in text, 'Missing release runtime configuration')
    require('BUILD SUCCESSFUL' in text and 'FAILED' not in text, 'Unsuccessful Maven dependency report')
    result = set()
    for match in re.finditer(r'(?:\+---|\\---) ([\w.-]+):([\w.-]+):([^\s]+)(?: -> ([^\s]+))?', text):
        group, artifact, version, resolved = match.groups()
        require(':' not in (resolved or ''), 'Unexpected dependency substitution')
        result.add(f'{group}:{artifact}:{resolved or version}')
    require(bool(result), 'Empty Maven dependency report')
    return result

def audit_maven(text, baseline):
    actual = maven_coordinates(text)
    expected = set(baseline['resolved_maven_coordinates'])
    require(actual == expected, f'Maven drift: added={sorted(actual-expected)}, removed={sorted(expected-actual)}')
    return sorted(actual)

def audit_apk(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        require(not any('dartjni' in name.lower() for name in names), 'Dart JNI native library in APK')
        require(any(name.endswith('/libiotools.so') for name in names), 'Application native engine missing')
        for name in names:
            if re.fullmatch(r'classes\d*\.dex', name):
                data = archive.read(name)
                require(b'Lcom/github/dart_lang/jni/' not in data, 'Dart JNI Java classes in APK')
                require(b'Ldev/dart/jni/' not in data, 'Dart JNI Java classes in APK')
    return {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--deps', type=Path, required=True)
    parser.add_argument('--maven', type=Path)
    parser.add_argument('--apk', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    baseline = json.loads(BASELINE.read_text())
    report = {'status': 'failed', 'baseline_source_commit': baseline['source_commit']}
    try:
        mobile = args.root / 'mobile'
        report['lock'] = audit_lock((mobile / 'pubspec.lock').read_text(), baseline)
        audit_graph(json.loads(args.deps.read_text()), report['lock']['versions'])
        report['android_plugins'] = audit_plugins(json.loads((mobile / '.flutter-plugins-dependencies').read_text()))
        if args.maven:
            report['maven_coordinates'] = audit_maven(args.maven.read_text(), baseline)
        if args.apk:
            report['apk'] = audit_apk(args.apk)
        report['status'] = 'passed'
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + '\n')
    print('Controlled Android dependency closure verified; runtime acceptance remains mandatory')

if __name__ == '__main__':
    main()
