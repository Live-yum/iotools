#!/usr/bin/env python3
"""Build and package the host-native TUI, including explicit Mach-O checks."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile
from release import ROOT, digest, require, write_json


def check_macos_dependencies(architectures, dependencies, arch):
    expected = {'amd64': 'x86_64', 'arm64': 'arm64'}[arch]
    require(architectures.split() == [expected], f'Expected one Mach-O {expected} slice')
    rows = dependencies.splitlines()
    require(len(rows) >= 2, 'Missing Mach-O dependency report')
    libraries = []
    for row in rows[1:]:
        path = row.strip().split(' (', 1)[0]
        require(path.startswith(('/usr/lib/', '/System/Library/Frameworks/')),
                'Unexpected non-system Mach-O dependency: ' + path)
        require('..' not in Path(path).parts, 'Unsafe Mach-O dependency path')
        libraries.append(path)
    require('/usr/lib/libSystem.B.dylib' in libraries, 'Expected macOS system runtime')
    return libraries


def run(*args, **kwargs):
    return subprocess.run(args, cwd=ROOT, check=True, **kwargs)


def main():
    p = argparse.ArgumentParser(); p.add_argument('target', choices=['linux', 'windows', 'macos']); p.add_argument('arch', choices=['amd64', 'arm64'])
    args = p.parse_args()
    sha = os.environ['IOTOOLS_SHA']
    goos = 'darwin' if args.target == 'macos' else args.target
    require(subprocess.check_output(['go', 'env', 'GOHOSTOS'], text=True, encoding="utf-8").strip() == goos, 'Use a native OS runner')
    require(subprocess.check_output(['go', 'env', 'GOHOSTARCH'], text=True, encoding="utf-8").strip() == args.arch, 'Use a native architecture runner')
    bundle = ROOT / 'dist' / ('tui-' + args.target + '-' + args.arch)
    bundle.mkdir(parents=True, exist_ok=False)
    binary = bundle / ('iotools.exe' if args.target == 'windows' else 'iotools')
    env = {**os.environ, 'GOOS': goos, 'GOARCH': args.arch, 'CGO_ENABLED': '0'}
    run('go', 'build', '-trimpath', '-ldflags', '-s -w -X main.version=' + sha, '-o', str(binary), './cmd/iotools', env=env)
    if args.target == 'macos':
        architecture = subprocess.check_output(['xcrun', 'lipo', '-archs', str(binary)], text=True, encoding="utf-8")
        dependencies = subprocess.check_output(['otool', '-L', str(binary)], text=True, encoding="utf-8")
        imports = check_macos_dependencies(architecture, dependencies, args.arch)
    else:
        run('go', 'run', './scripts/verifybinary', str(binary))
        imports = 'Existing ELF/PE standalone verifier passed'
    version = subprocess.check_output([str(binary), '--version'], text=True, encoding="utf-8")
    require(sha in version, 'Built executable does not expose exact source SHA')
    run(str(binary), '--file', 'examples/local.yaml', '--validate')
    smoke = bundle / 'smoke.yaml'
    run(str(binary), '--file', str(smoke), '--init')
    run(str(binary), '--file', str(smoke), '--validate')
    smoke.unlink()
    run('go', 'run', './scripts/notices', '-goos', goos, '-goarch', args.arch, '-cgo', '0', str(bundle / 'THIRD_PARTY_LICENSES'), './cmd/iotools')
    for name in ('LICENSE', 'README.md'):
        shutil.copyfile(ROOT / name, bundle / name)
    for name in ('docs', 'examples'):
        shutil.copytree(ROOT / name, bundle / name)
    (bundle / 'SHA256SUMS').write_text(f'{digest(binary)}  {binary.name}\n', encoding='utf-8')
    archive = bundle.with_suffix('.zip')
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
        for file in sorted(bundle.rglob('*')):
            if file.is_file():
                z.write(file, str(Path(bundle.name) / file.relative_to(bundle)))
    write_json(bundle.with_suffix('.json'), {'source_sha': sha, 'platform': args.target + '-' + args.arch,
               'file': archive.name, 'sha256': digest(archive), 'bytes': archive.stat().st_size,
               'binary_sha256': digest(binary), 'standalone_runtime_verified': True, 'smoke_passed': True,
               'dependencies': imports, 'toolchain': subprocess.check_output(['go', 'version'], text=True, encoding="utf-8").strip(),
               'scope': 'Native build, architecture/runtime checks and CLI smoke. macOS interactive TUI UI not separately accepted.'})


if __name__ == '__main__':
    main()
