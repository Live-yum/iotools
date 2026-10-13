#!/usr/bin/env python3
"""Fail closed if the reviewed Android path-provider/JNI upgrade drifts."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import zipfile

ROOT = Path(__file__).resolve().parents[2]
BASELINE = Path(__file__).with_name('android_dependency_baseline.json')
UPDATES = Path(__file__).with_name('android_dependency_updates.json')
JNI = {'jni', 'jni_flutter', 'jni_util'}
JNI_CLOSURE = JNI | {'args', 'package_config'}
JNI_DEPENDENCIES = {
    'path_provider_android': {'flutter', 'jni', 'jni_flutter', 'path_provider_platform_interface'},
    'jni': {'args', 'collection', 'ffi', 'jni_util', 'meta', 'package_config', 'path', 'plugin_platform_interface'},
    'jni_flutter': {'flutter', 'jni'},
    'jni_util': {'path'},
}
ANDROID_PLUGINS = {'file_selector_android': True, 'integration_test': True,
                   'path_provider_android': False, 'jni': True, 'jni_flutter': True}
JNI_PLUGIN_DEPENDENCIES = {'path_provider_android': {'jni', 'jni_flutter'},
                           'jni': set(), 'jni_flutter': {'jni'}}
JNI_CLASSES = {b'Lcom/github/dart_lang/jni/JniPlugin;',
               b'Lcom/github/dart_lang/jni_flutter/JniFlutterPlugin;',
               *(f'Lcom/github/dart_lang/jni/{name};'.encode() for name in
                 ('JniUtils', 'PortCleaner', 'PortContinuation', 'PortProxyBuilder'))}

def require(condition, message):
    if not condition:
        raise ValueError(message)

def reviewed_baseline(baseline, updates):
    """Apply only checked-in exact changes; keep the original evidence intact."""
    baseline = json.loads(json.dumps(baseline))
    require(updates['schema'] == 2, 'Unsupported reviewed dependency update schema')
    require(updates['baseline_source_commit'] == baseline['source_commit'], 'Wrong update baseline')
    migration = updates['android_implementation']
    require(migration['from_isolation_version'] == '2.2.23' and migration['version'] == '2.3.1',
            'Unreviewed Android implementation migration')
    require(set(migration['restored_closure']) == JNI_CLOSURE == set(baseline['allowed_removed'])
            and len(migration['restored_closure']) == len(JNI_CLOSURE), 'Unexpected restored JNI closure')
    require(re.fullmatch(r'[0-9a-f]{64}', migration['archive_sha256']), 'Invalid Android archive hash')
    require(migration['ndk_version'] == '28.1.13356709', 'Unreviewed JNI NDK version')
    require('path_provider_android' in updates['lock_updates'], 'Missing exact Android lock update')
    baseline['allowed_removed'] = []
    baseline['path_provider_android_archive_sha256'] = migration['archive_sha256']
    for name, change in updates['lock_updates'].items():
        require(baseline['lock_versions'].get(name) == change['from'], 'Wrong prior dependency version')
        require(re.fullmatch(r'[0-9a-f]{64}', change['lock_entry_sha256']), 'Invalid updated lock hash')
        baseline['lock_versions'][name] = change['to']
        baseline['lock_entry_sha256'][name] = change['lock_entry_sha256']
    require(baseline['lock_versions']['path_provider_android'] == '2.3.1', 'Unreviewed Android package version')
    require(baseline['lock_versions']['path_provider'] == '2.1.6', 'Unreviewed path-provider version')
    coordinates = baseline['resolved_maven_coordinates']
    for before, after in updates['maven_updates'].items():
        require(before in coordinates, 'Wrong prior Maven coordinate')
        require(before.rsplit(':', 1)[0] == after.rsplit(':', 1)[0], 'Changed Maven artifact identity')
        coordinates[coordinates.index(before)] = after
    for coordinate in updates.get('maven_added', []):
        require(coordinate not in coordinates, 'Duplicate added Maven coordinate')
        coordinates.append(coordinate)
    return baseline

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
    require(current.get('path_provider_android') == '2.3.1', 'Expected path_provider_android 2.3.1')
    require(JNI_CLOSURE <= current.keys(), 'Reviewed Dart JNI closure missing')
    blocks = dict(re.findall(r'^  (\w+):\n((?:    .*\n|\n)+)', text, re.M))
    if 'lock_entry_sha256' in baseline:
        for name, body in blocks.items():
            require(hashlib.sha256(body.encode()).hexdigest() == baseline['lock_entry_sha256'].get(name),
                    f'Unreviewed lock entry changed: {name}')
        provider = blocks['path_provider_android']
        require('    dependency: "direct main"\n' in provider and '    source: hosted\n' in provider
                and '      url: "https://pub.dev"\n' in provider
                and re.search(r'      sha256: "?' + baseline['path_provider_android_archive_sha256'] + r'"?\n', provider),
                'Expected exact official hosted path-provider archive')
    removed = set(previous) - current.keys()
    require(removed == set(baseline['allowed_removed']), f'Unexpected removed closure: {sorted(removed)}')
    require(not (current.keys() - previous.keys()), 'Unexpected added dependency')
    drift = {name: (previous[name], version) for name, version in current.items()
             if previous[name] != version}
    require(not drift, f'Unreviewed dependency versions changed: {drift}')
    return {'package_count': len(current), 'removed': sorted(removed), 'versions': current}

def audit_graph(graph, versions):
    entries = graph['packages']
    names = [entry['name'] for entry in entries]
    require(len(names) == len(set(names)), 'Duplicate dependency graph entries')
    require(JNI_CLOSURE <= set(names), 'Reviewed JNI resolver closure missing')
    resolved = {entry['name']: entry['version'] for entry in entries if entry['kind'] != 'root'}
    require(resolved == versions, 'Resolver graph does not match lockfile')
    for name, dependencies in JNI_DEPENDENCIES.items():
        actual = next(entry for entry in entries if entry['name'] == name)['dependencies']
        require(set(actual) == dependencies and len(actual) == len(dependencies),
                f'Unexpected {name} dependencies')

def audit_plugins(data):
    android = data['plugins']['android']
    names = {entry['name'] for entry in android}
    require(names == ANDROID_PLUGINS.keys() and len(android) == len(names),
            f'Unexpected Android plugin set: {sorted(names)}')
    for entry in android:
        require(entry['native_build'] is ANDROID_PLUGINS[entry['name']],
                f'Unexpected native registration: {entry["name"]}')
    graph = data['dependencyGraph']
    require(len({entry['name'] for entry in graph}) == len(graph), 'Duplicate Flutter plugin graph entry')
    require(JNI & {entry['name'] for entry in graph} == {'jni', 'jni_flutter'},
            'Unexpected JNI plugin closure')
    for name, dependencies in JNI_PLUGIN_DEPENDENCIES.items():
        entry = next((entry for entry in graph if entry['name'] == name), None)
        require(entry is not None and set(entry['dependencies']) == dependencies
                and len(entry['dependencies']) == len(dependencies), f'Unexpected {name} plugin graph')
        entry = next(entry for entry in android if entry['name'] == name)
        require(set(entry['dependencies']) == dependencies and len(entry['dependencies']) == len(dependencies),
                f'Unexpected {name} Android registration dependencies')
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

def dex_defined_classes(data):
    """Read class_defs, not string references that could name missing classes."""
    require(len(data) >= 112 and re.fullmatch(rb'dex\n0(?:3[5-9]|40)\x00', data[:8]), 'Unsupported DEX header')
    size, header, endian = struct.unpack_from('<III', data, 32)
    require(size == len(data) and header == 112 and endian == 0x12345678, 'Invalid DEX size or byte order')
    strings, string_off, types, type_off = struct.unpack_from('<IIII', data, 56)
    classes, class_off = struct.unpack_from('<II', data, 96)
    for count, offset, width in ((strings, string_off, 4), (types, type_off, 4), (classes, class_off, 32)):
        require(count == 0 or (offset >= header and offset + count * width <= len(data)), 'DEX table outside file')
    result = set()
    for index in range(classes):
        type_index = struct.unpack_from('<I', data, class_off + index * 32)[0]
        require(type_index < types, 'DEX class type outside table')
        string_index = struct.unpack_from('<I', data, type_off + type_index * 4)[0]
        require(string_index < strings, 'DEX class string outside table')
        offset = struct.unpack_from('<I', data, string_off + string_index * 4)[0]
        require(header <= offset < len(data), 'DEX class name outside file')
        # Skip the bounded ULEB128 UTF-16 length. Descriptors are NUL-terminated MUTF-8.
        for byte_index in range(5):
            require(offset < len(data), 'Truncated DEX string length')
            value = data[offset]
            offset += 1
            if value < 128:
                break
        else:
            raise ValueError('Invalid DEX string length')
        end = data.find(b'\x00', offset)
        require(end >= offset, 'Unterminated DEX class descriptor')
        descriptor = data[offset:end]
        require(descriptor.startswith(b'L') and descriptor.endswith(b';'), 'Invalid DEX class descriptor')
        require(descriptor not in result, 'Duplicate DEX class definition')
        result.add(descriptor)
    return result

def audit_apk(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        require(len(names) == len(set(names)), 'Duplicate APK entries')
        app_abis = {name.split('/')[1] for name in names
                    if re.fullmatch(r'lib/[^/]+/libiotools\.so', name)}
        require(bool(app_abis) and app_abis <= {'arm64-v8a', 'x86_64'}, 'Application native engine ABI missing or unexpected')
        expected = {f'lib/{abi}/libdartjni.so' for abi in app_abis}
        actual = {name for name in names if 'dartjni' in name.lower()}
        require(actual == expected, f'Unexpected Dart JNI native library set: {sorted(actual)}')
        for abi in app_abis:
            name = f'lib/{abi}/libdartjni.so'
            require(archive.getinfo(name).compress_type == zipfile.ZIP_DEFLATED,
                    f'JNI library must use standard installer extraction: {abi}')
            data = archive.read(name)
            require(len(data) >= 64 and data[:6] == b'\x7fELF\x02\x01', f'Expected little-endian ELF64 JNI library: {abi}')
            require(struct.unpack_from('<H', data, 18)[0] == {'arm64-v8a': 183, 'x86_64': 62}[abi],
                    f'Wrong JNI machine architecture: {abi}')
            phoff = struct.unpack_from('<Q', data, 32)[0]
            phsize, phnum = struct.unpack_from('<HH', data, 54)
            require(phsize >= 56 and phnum > 0 and phoff >= 64 and phoff + phsize * phnum <= len(data),
                    f'Invalid JNI ELF program headers: {abi}')
            loads = 0
            for index in range(phnum):
                kind, flags, offset, vaddr, paddr, filesz, memsz, align = struct.unpack_from(
                    '<IIQQQQQQ', data, phoff + index * phsize)
                if kind == 1:
                    loads += 1
                    require(align >= 16384 and align & (align - 1) == 0
                            and offset % align == vaddr % align and filesz <= memsz
                            and offset + filesz <= len(data), f'JNI load segment is not 16KiB compatible: {abi}')
            require(loads > 0, f'JNI ELF has no load segment: {abi}')
        classes = set()
        for name in names:
            if re.fullmatch(r'classes\d*\.dex', name):
                data = archive.read(name)
                defined = dex_defined_classes(data)
                require(not classes & defined, 'Duplicate classes across APK DEX files')
                classes |= defined
                require(not any(value.startswith(b'Ldev/dart/jni/') for value in defined),
                        'Unexpected alternate JNI Java namespace')
        require(JNI_CLASSES <= classes, 'Required JNI Java class definitions missing')
    return {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
            'jni_abis': sorted(app_abis), 'jni_classes': sorted(value.decode() for value in JNI_CLASSES)}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--deps', type=Path, required=True)
    parser.add_argument('--maven', type=Path)
    parser.add_argument('--apk', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    report = {'status': 'failed'}
    try:
        baseline = reviewed_baseline(json.loads(BASELINE.read_text()), json.loads(UPDATES.read_text()))
        report['baseline_source_commit'] = baseline['source_commit']
        report['reviewed_updates_sha256'] = hashlib.sha256(UPDATES.read_bytes()).hexdigest()
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
