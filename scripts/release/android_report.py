#!/usr/bin/env python3
"""Record verified normal-entry AOT APK scope and embedded license completeness."""
import json
import os
from pathlib import Path
import re
import sys
import zipfile
from release import digest, require, write_json


def check_notices(apk):
    names = set(apk.namelist())
    required = {'assets/LICENSE', 'assets/android.md', 'assets/licenses/usb-serial-for-android.txt',
                'assets/THIRD_PARTY_LICENSES/INDEX.txt', 'assets/THIRD_PARTY_LICENSES/MANIFEST.json',
                'assets/flutter_assets/NOTICES.Z'}
    require(required <= names, 'Missing embedded Android/Go/Flutter licenses: ' + repr(sorted(required - names)))
    manifest = json.loads(apk.read('assets/THIRD_PARTY_LICENSES/MANIFEST.json'))
    require(bool(manifest['Licenses']), 'Empty Android Go licenses')
    import hashlib
    for row in manifest['Licenses']:
        path = Path(row['File'])
        require(not path.is_absolute() and '..' not in path.parts, 'Unsafe license path')
        require(hashlib.sha256(apk.read('assets/THIRD_PARTY_LICENSES/' + path.as_posix())).hexdigest() == row['SHA256'], 'Embedded license checksum mismatch')
    return {'location': 'APK assets: project + Android/USB + exact Go dependencies + Flutter NOTICES.Z',
            'go_license_count': len(manifest['Licenses'])}


def signing_fingerprint(signature):
    require(re.search(r'(?m)^Number of signers: 1\s*$', signature), 'Expected exactly one APK signer')
    require('CN=Android Debug' in signature, 'Unexpected APK signer; review signing scope before release')
    # Build-tools print either legacy Signer #1 or scheme-specific V2 Signer.
    certs = re.findall(r'(?m)^(?:Signer #1|V[1-4](?:\.[0-9]+)? Signer:) certificate SHA-256 digest: ([0-9a-fA-F]{64})\s*$', signature)
    require(bool(certs) and len({value.lower() for value in certs}) == 1,
            'Missing or inconsistent single signing certificate fingerprint')
    return certs[0].lower()


def main():
    abi, filename = sys.argv[1:]
    require(abi in ('arm64-v8a', 'universal'), 'Unexpected APK ABI')
    asset = Path(filename)
    with zipfile.ZipFile(asset) as apk:
        licenses = check_notices(apk)
        abis = ['arm64-v8a'] if abi == 'arm64-v8a' else ['arm64-v8a', 'x86_64']
        for arch in abis:
            require(os.environ['IOTOOLS_SHA'].encode() in apk.read(f'lib/{arch}/libiotools.so'), 'Native core source revision absent')
    signature = Path(f'android-evidence/{abi}-signature.txt').read_text(encoding="utf-8")
    certificate = signing_fingerprint(signature)
    write_json(f'release-android/{abi}.json', {'source_sha': os.environ['IOTOOLS_SHA'], 'abis': abis,
               'sha256': digest(asset), 'bytes': asset.stat().st_size, 'normal_entry_aot': True, 'test_code_absent': True,
               'signing': 'debug/test certificate; not production publisher identity',
               'signer_certificate_sha256': certificate, 'licenses': licenses,
               'scope': 'Exact source normal main.dart Release/AOT with historical universal/ARM64 build flags. Static code, permission, alignment and signature checks; no final-commit emulator runtime rerun. Historical runtime is source-equivalent, never the same delivery binary.'})


if __name__ == '__main__':
    main()
