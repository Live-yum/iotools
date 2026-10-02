#!/usr/bin/env python3
"""Fail-closed source, asset and draft-publication gates for the v0.3.1 release.

No runtime acceptance is manufactured for the release SHA. Historical results
retain their original SHAs; only the explicitly inventoried source inputs match.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
REPOSITORY = 'Live-yum/iotools'
VERSION = 'v0.3.1'
FLUTTER_REVISION = 'adc901062556672b4138e18a4dc62a4be8f4b3c2'
PLATFORMS = ('linux-amd64', 'linux-arm64', 'windows-amd64', 'macos-amd64', 'macos-arm64')
EXPECTED = {f'{kind}-{platform}': '.zip' for kind in ('tui', 'flutter', 'web') for platform in PLATFORMS}
EXPECTED.update({'android-arm64-v8a-aot-test-signed': '.apk', 'android-universal-arm64-x86_64-aot-test-signed': '.apk',
                 'ios-device-arm64-unsigned': '.zip', 'ios-simulator-arm64-debug-developer': '.zip'})
# v0.3.1 uses current-candidate acceptance, so no historical harness or release
# builder exclusions are needed. A parent must have the exact complete Git tree.
EXCLUDED_FILES = set()
HISTORY = [
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


def require(value, message):
    if not value:
        raise RuntimeError(message)


def git(*args, root=ROOT):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True, encoding="utf-8").strip()


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def write_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')


def is_input(path):
    return path not in EXCLUDED_FILES


def inventory(revision, root=ROOT):
    raw = subprocess.check_output(['git', '-C', str(root), 'ls-tree', '-r', '-z', revision])
    entries = {}
    for entry in raw.split(b'\0'):
        if not entry:
            continue
        meta, path = entry.split(b'\t', 1)
        mode, kind, sha = meta.decode().split()
        path = path.decode('utf-8')
        require(kind == 'blob', f'Unreviewed non-blob input: {path}')
        entries[path] = {'mode': mode, 'git_blob': sha}
    return entries


def inputs_digest(tree):
    inputs = {path: row for path, row in tree.items() if is_input(path)}
    return hashlib.sha256(json.dumps(inputs, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def compare_sources(candidate, baseline, root=ROOT):
    before, after = inventory(baseline, root), inventory(candidate, root)
    changed = sorted(p for p in before.keys() | after.keys() if before.get(p) != after.get(p))
    unsafe = [p for p in changed if is_input(p)]
    require(not unsafe, f'Historical runtime source/build inputs differ from {baseline}: {unsafe}')
    require(inputs_digest(before) == inputs_digest(after), 'Source input digest mismatch')
    return {'baseline_sha': baseline, 'candidate_sha': candidate, 'identical_input_sha256': inputs_digest(after),
            'baseline_tree': git('rev-parse', baseline + '^{tree}', root=root),
            'candidate_tree': git('rev-parse', candidate + '^{tree}', root=root),
            'excluded_differences': {p: {'before': before.get(p), 'after': after.get(p)} for p in changed},
            'claim': ('same source commit; packages rebuilt independently' if candidate == baseline else
                      'same application/build-input Git blobs across commits; packages rebuilt independently')}


def identity(sha, version=VERSION, root=ROOT):
    require(re.fullmatch('[0-9a-f]{40}', sha), 'Expected full source commit SHA')
    require(version == VERSION, 'Only the reviewed v0.3.1 release is supported')
    require(git('rev-parse', 'HEAD', root=root) == sha, 'Checkout is not the declared source SHA')
    require(re.search(r'(?m)^version: 0\.3\.1\+4\s*$', (root / 'mobile/pubspec.yaml').read_text(encoding="utf-8")),
            'Tag and Flutter application version disagree')
    return {'source_sha': sha, 'tag': version, 'source_inputs_sha256': inputs_digest(inventory(sha, root))}


def check_worktree(root=ROOT):
    # CocoaPods generates fresh object IDs each run. Ignore only those IDs and
    # formatting, never settings/scripts/references; compare reviewed semantics.
    from pbx_gate import normalized_digest
    projects = {
        'mobile/ios/Runner.xcodeproj/project.pbxproj': (
            'cfb367aba9a5ceff94dfe887876d6e604a03abdd6bd22901c213f1a838e01f82',
            '23b5876c6356eec3343741459df79c6a773d0845f0a03d28d9acb776a9a18214'),
        'mobile/macos/Runner.xcodeproj/project.pbxproj': (
            '217137f1742343862f1d039ec599c095af514ab672408dffa514471faaa6bc86',
            'ebdfd9e343466df2260882b86bcc7b01af2df6aba9cde0934ac956b3ef257813'),
    }
    workspaces = {'mobile/ios/Runner.xcworkspace/contents.xcworkspacedata',
                  'mobile/macos/Runner.xcworkspace/contents.xcworkspacedata'}
    changed = git('diff', '--name-only', 'HEAD', root=root).splitlines()
    evidence = {}
    for name in changed:
        raw = subprocess.check_output(['git', '-C', str(root), 'show', 'HEAD:' + name])
        original, generated = hashlib.sha256(raw).hexdigest(), digest(root / name)
        out = root / 'platform-evidence/release-generated' / name
        out.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(root / name, out)
        diff = git('diff', 'HEAD', '--', name, root=root)
        Path(str(out) + '.diff').write_text(diff, encoding='utf-8')
        row = {'original_sha256': original, 'generated_sha256': generated, 'diff': diff}
        evidence[name] = row
        accepted = False
        if name in projects:
            row['normalized_generated_sha256'] = normalized_digest(raw.decode('utf-8'), (root / name).read_text(encoding='utf-8'))
            accepted = (original, row['normalized_generated_sha256']) == projects[name]
        elif name in workspaces:
            accepted = (original, generated) == (
                '46c3c9702ff7c5584e11c954d943cfb6e07fb26e3fbd072ddbc0f388aa352de1',
                '465e6de5660384eb6832e8078920ba6cd5305c445cb29a24a86a2fda4aa6b67e')
        write_json(root / 'platform-evidence/release-generated/mutations.json', evidence)
        require(accepted, f'Unreviewed tracked build mutation: {name} ({original} -> {generated})')
    return evidence


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(urllib.parse.urlsplit(newurl).scheme == 'https', 'Insecure asset redirect')
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and urllib.parse.urlsplit(req.full_url).netloc != urllib.parse.urlsplit(newurl).netloc:
            redirected.remove_header('Authorization')
        return redirected


class GitHub:
    def __init__(self, token=None):
        self.token = token or os.environ['GH_TOKEN']

    def request(self, path, method='GET', data=None, raw=False):
        url = path if path.startswith('https://') else 'https://api.github.com/repos/' + REPOSITORY + path
        require(url.startswith(('https://api.github.com/repos/' + REPOSITORY + '/',
                                'https://uploads.github.com/repos/' + REPOSITORY + '/')), 'Unexpected GitHub API destination')
        headers = {'Authorization': 'Bearer ' + self.token, 'X-GitHub-Api-Version': '2022-11-28',
                   'Accept': 'application/octet-stream' if raw else 'application/vnd.github+json',
                   'User-Agent': 'iotools-release-gate'}
        if isinstance(data, Path):
            headers['Content-Type'] = 'application/octet-stream'
            body = data.read_bytes()
        elif data is not None:
            headers['Content-Type'] = 'application/json'
            body = json.dumps(data).encode()
        else:
            body = None
        # Never automatically retry mutation requests with uncertain outcomes.
        with urllib.request.build_opener(SafeRedirect()).open(urllib.request.Request(url, body, headers, method=method), timeout=180) as response:
            content = response.read()
        return content if raw else json.loads(content)

    def optional(self, path):
        try:
            return self.request(path)
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            raise

    def jobs(self, run_id):
        rows, page = [], 1
        while True:
            batch = self.request(f'/actions/runs/{run_id}/jobs?filter=latest&per_page=100&page={page}')['jobs']
            rows.extend(batch)
            if len(batch) < 100:
                return rows
            page += 1


def verify_historical(api, spec):
    run = api.request(f'/actions/runs/{spec["run_id"]}')
    require(run['repository']['full_name'] == REPOSITORY and run['head_sha'] == spec['sha'], 'Historical run repository/SHA mismatch')
    require(run['path'] == spec['workflow'], 'Historical workflow path mismatch')
    require(run['status'] == 'completed', 'Historical workflow is not complete')
    if spec['whole_run_success']:
        require(run['conclusion'] == 'success', 'Historical workflow is not successful')
    jobs = api.jobs(spec['run_id'])
    selected = []
    for name in spec['jobs']:
        found = [job for job in jobs if job['name'] == name]
        require(len(found) == 1 and found[0]['status'] == 'completed' and found[0]['conclusion'] == 'success', f'Historical job not successful: {name}')
        job = found[0]
        required_steps = spec['steps'] + spec.get('job_steps', {}).get(name, [])
        for step in required_steps:
            matches = [row for row in job['steps'] if row['name'] == step]
            require(len(matches) == 1 and matches[0]['conclusion'] == 'success', f'Historical gate missing/failed: {step}')
        selected.append({'id': job['id'], 'name': name, 'conclusion': job['conclusion'], 'required_steps': required_steps})
    return {**spec, 'url': run['html_url'], 'run_attempt': run['run_attempt'], 'run_conclusion': run['conclusion'], 'verified_jobs': selected,
            'scope': 'runtime acceptance at the recorded SHA; final packages are rebuilt from the tag commit'}


def resolve_tag(api, version):
    ref = api.optional('/git/ref/tags/' + version)
    if ref is None:
        return None
    obj = ref['object']
    seen = set()
    while obj['type'] == 'tag':
        require(obj['sha'] not in seen and len(seen) < 8, 'Invalid annotated tag chain')
        seen.add(obj['sha'])
        obj = api.request('/git/tags/' + obj['sha'])['object']
    require(obj['type'] == 'commit', 'Release tag does not reference a commit')
    return obj['sha']


def publication_context(api, sha, event, ref, publish):
    require(event in ('push', 'workflow_dispatch'), 'Unsupported release event')
    if event == 'push':
        if ref.startswith('refs/tags/'):
            require(ref == 'refs/tags/' + VERSION and resolve_tag(api, VERSION) == sha, 'Tag event does not match exact source commit')
        else:
            require(False, 'Branch push cannot trigger a release build; use an explicit dry-run dispatch')
    elif publish:
        require(ref == 'refs/heads/main', 'Manual publishing is allowed only from main')
    if publish:
        # Never infer successful merge just from the workflow's branch label.
        main = api.request('/git/ref/heads/main')['object']['sha']
        comparison = api.request('/compare/' + sha + '...' + main)
        require(comparison['status'] in ('identical', 'ahead'), 'Release source is not merged into main')
        tag_sha = resolve_tag(api, VERSION)
        require(tag_sha in (None, sha), 'Existing release tag targets a different SHA')
        require(api.optional('/releases/tags/' + VERSION) is None, 'Release already exists; no overwrite or silent retry')


class MissingAcceptance(RuntimeError):
    pass


def acceptance_for_source(api, sha):
    """Use every latest required workflow on one exact source commit."""
    rows = api.request(f'/actions/runs?head_sha={sha}&per_page=100')['workflow_runs']
    require(len(rows) < 100, 'Acceptance run pagination requires review')
    accepted, missing = [], []
    for template in HISTORY:
        matches = [row for row in rows if row['head_sha'] == sha and row['path'] == template['workflow']]
        if not matches:
            missing.append(template['workflow'])
            continue
        # Never pick an older successful run over a newer failed/pending one.
        latest = max(matches, key=lambda row: row['id'])
        spec = {**template, 'sha': sha, 'run_id': latest['id']}
        accepted.append(verify_historical(api, spec))
    if missing:
        raise MissingAcceptance(f'Missing current-source acceptance at {sha}: {missing}')
    return accepted


def verify_acceptance(api, sha, root=ROOT):
    # A merge commit can inherit a completely verified, identical PR tree.
    # Only the commit itself or its direct parents qualify, never an arbitrary
    # old successful build or a source revision with changed app/build inputs.
    candidates = git('rev-list', '--parents', '-n', '1', sha, root=root).split()
    errors = []
    for candidate in candidates:
        try:
            comparison = compare_sources(sha, candidate, root)
        except RuntimeError as error:
            errors.append(str(error))
            continue
        try:
            accepted = acceptance_for_source(api, candidate)
        except MissingAcceptance as error:
            errors.append(str(error))
            continue
        # A failed/pending latest run is NOT eligible for parent fallback.
        return accepted, comparison
    raise RuntimeError('No complete current-source runtime acceptance: ' + '; '.join(errors))


def preflight(args):
    result = identity(args.sha)
    api = GitHub()
    publication_context(api, args.sha, os.environ['GITHUB_EVENT_NAME'], os.environ['GITHUB_REF'], args.publish)
    accepted, comparison = verify_acceptance(api, args.sha)
    result.update(historical=accepted, source_equivalence=[comparison],
                  excluded_files=sorted(EXCLUDED_FILES),
                  flutter_revision=FLUTTER_REVISION, run_id=os.environ['GITHUB_RUN_ID'],
                  run_attempt=os.environ['GITHUB_RUN_ATTEMPT'], publish_requested=args.publish)
    write_json(args.output, result)
    print(json.dumps(result, ensure_ascii=False))


def filename(key):
    require(key in EXPECTED, 'Unexpected release asset type: ' + key)
    return 'iotools-' + VERSION + '-' + key + EXPECTED[key]


def validate_build_report(key, report, asset, sha):
    require(report.get('source_sha') == sha, 'Package report source SHA mismatch')
    require(report.get('sha256') == digest(asset) and report.get('bytes') == asset.stat().st_size, 'Package report hash/size mismatch')
    if key.startswith('ios-device'):
        require(report.get('kind') == 'device' and report.get('status') == 'passed_for_stated_scope', 'Device static gate failed')
        require(report.get('architectures') == ['arm64'] and report.get('signature', '').startswith('unsigned;'), 'Device scope/signature mismatch')
        require(report.get('production_isolation') and report.get('dependency_isolation'), 'Device production isolation missing')
    elif key.startswith('ios-simulator'):
        require(report.get('kind') == 'simulator-compiled' and report.get('status') == 'compiled_only_no_runtime_acceptance', 'Simulator artifact must retain compiled-only scope')
        require(report.get('architectures') == ['arm64'], 'Simulator must be arm64 only')
    elif key.startswith('android'):
        require(report.get('normal_entry_aot') is True and report.get('test_code_absent') is True, 'Normal-entry AOT APK gates missing')
        require(report.get('abis') == (['arm64-v8a'] if 'arm64-v8a' in key else ['arm64-v8a', 'x86_64']), 'Android ABI mismatch')
        require(report.get('signing') == 'debug/test certificate; not production publisher identity', 'Android signing disclosure missing')
    elif key.startswith('tui'):
        require(report.get('standalone_runtime_verified') is True and report.get('smoke_passed') is True, 'TUI runtime/smoke gates missing')
        require(report.get('platform') == key[len('tui-'):], 'TUI target mismatch')
    elif key.startswith('flutter'):
        require(report.get('platform') + '-' + report.get('runner_arch') == key[len('flutter-'):], 'Flutter target mismatch')
        require(report.get('native_core_sha256'), 'Native core report missing')
    elif key.startswith('web'):
        require(report.get('platform') + '-' + report.get('arch') == key[len('web-'):], 'Web target mismatch')
        require(report.get('binary_sha256'), 'Web gateway report missing')


def embedded_revision(asset, key, sha):
    """Inspect actual shipped Go binaries, not only filenames or JSON metadata."""
    if key.startswith('android'):
        abis = ['arm64-v8a'] if 'arm64-v8a' in key else ['arm64-v8a', 'x86_64']
        names = [f'lib/{abi}/libiotools.so' for abi in abis]
        platform = 'android'
    else:
        abis = None
        platform = key.split('-')[1]
        names = None
    with zipfile.ZipFile(asset) as archive:
        if names is None:
            suffix = ('/Runner.app/Runner' if key.startswith('ios') else
                      '/iotools.exe' if key.startswith('tui-windows') else
                      '/iotools' if key.startswith('tui') else
                      '/iotools-web.exe' if key.startswith('web-windows') else
                      '/iotools-web' if key.startswith('web') else
                      '/iotools_native.dll' if key.startswith('flutter-windows') else
                      '/libiotools_native.dylib' if key.startswith('flutter-macos') else '/libiotools_native.so')
            names = [n for n in archive.namelist() if ('/' + n).endswith(suffix) and not n.startswith('__MACOSX/')]
            require(len(names) == 1, 'Package must contain exactly one expected Go binary')
        result = []
        for index, name in enumerate(names):
            data = archive.read(name)
            require(sha.encode() in data, 'Shipped Go binary is missing exact source revision: ' + name)
            row = {'file': name, 'sha256': hashlib.sha256(data).hexdigest(), 'embedded_source_sha': sha}
            if key.startswith('ios'):
                row['build_info_scope'] = 'Linked iOS Runner contains the exact Go version stamp; c-archive has no standalone go-version report'
            else:
                with tempfile.TemporaryDirectory() as folder:
                    file = Path(folder) / 'binary'
                    file.write_bytes(data)
                    info = subprocess.check_output(['go', 'version', '-m', str(file)], text=True, encoding="utf-8")
                expected_os = 'darwin' if platform == 'macos' else platform
                expected_arch = ('arm64' if abis[index] == 'arm64-v8a' else 'amd64') if abis else key.rsplit('-', 1)[1]
                # Go omits -ldflags from buildinfo with -trimpath. Require the
                # actual VCS revision plus target; raw embedded SHA is checked above.
                settings = dict(line.strip().split("=", 1) for line in info.splitlines()
                                if line.strip().startswith("build\t") and "=" in line)
                settings = {key.removeprefix("build\t"): value for key, value in settings.items()}
                require(settings.get("vcs.revision") == sha
                        and settings.get("GOOS") == expected_os
                        and settings.get("GOARCH") == expected_arch,
                        'Shipped Go build identity/target differs: ' + repr(settings))
                row['go_build_info'] = info.replace(str(file), name)
            result.append(row)
    return result


def stage(args):
    record = identity(args.sha)
    generated = check_worktree()
    asset, report = Path(args.asset), json.loads(Path(args.report).read_text(encoding="utf-8"))
    require(asset.is_file() and not asset.is_symlink(), 'Missing/unsafe package')
    validate_build_report(args.key, report, asset, args.sha)
    embedded = embedded_revision(asset, args.key, args.sha)
    target = Path(args.output) / args.key
    target.mkdir(parents=True, exist_ok=False)
    destination = target / filename(args.key)
    shutil.copyfile(asset, destination)
    record.update(key=args.key, file=destination.name, sha256=digest(destination), bytes=destination.stat().st_size,
                  build_report=report, embedded_binaries=embedded, generated_project=generated, run_id=os.environ['GITHUB_RUN_ID'], run_attempt=os.environ['GITHUB_RUN_ATTEMPT'],
                  runner_os=os.environ['RUNNER_OS'], runner_arch=os.environ['RUNNER_ARCH'])
    write_json(target / 'receipt.json', record)


def assemble(folder, output, proof, sha):
    expected_identity = identity(sha)
    require(proof['source_sha'] == sha and proof['tag'] == VERSION
            and proof['source_inputs_sha256'] == expected_identity['source_inputs_sha256'], 'Preflight identity mismatch')
    receipts, seen = [], set()
    for receipt in sorted(Path(folder).rglob('receipt.json')):
        require(not receipt.is_symlink(), 'Unsafe receipt')
        record = json.loads(receipt.read_text(encoding="utf-8"))
        key = record['key']
        require(key in EXPECTED and key not in seen, 'Unexpected/duplicate asset key: ' + key)
        require(record['file'] == filename(key), 'Unexpected/unsafe asset filename')
        for field, value in expected_identity.items():
            require(record.get(field) == value, 'Asset identity differs: ' + field)
        for field in ('run_id', 'run_attempt'):
            require(record.get(field) == proof[field], 'Asset belongs to a different workflow run/attempt')
        asset = receipt.parent / record['file']
        require(asset.is_file() and not asset.is_symlink(), 'Missing/unsafe asset')
        require(set(p.name for p in receipt.parent.iterdir()) == {'receipt.json', asset.name}, 'Unexpected files in asset artifact')
        require(digest(asset) == record['sha256'] and asset.stat().st_size == record['bytes'], 'Asset transfer checksum mismatch')
        validate_build_report(key, record['build_report'], asset, sha)
        require(record.get('embedded_binaries'), 'Missing shipped binary identity report')
        require(embedded_revision(asset, key, sha) == record['embedded_binaries'], 'Shipped binary identity changed after staging')
        seen.add(key)
        receipts.append(record)
    require(seen == set(EXPECTED), 'Missing release assets: ' + repr(sorted(set(EXPECTED) - seen)))
    output = Path(output)
    output.mkdir(parents=True, exist_ok=False)
    for receipt in Path(folder).rglob('receipt.json'):
        row = json.loads(receipt.read_text(encoding="utf-8"))
        shutil.copyfile(receipt.parent / row['file'], output / row['file'])
    manifest = {**proof, 'schema': 1, 'assets': receipts, 'runtime_verification':
                'Every package is rebuilt from the tag commit. Latest complete Android, TUI, HTTP/UI/icon and all-eight-platform runtime gates are recorded at the same source commit or an identical direct-parent tree. See original SHAs and package scopes.'}
    write_json(output / 'manifest.json', manifest)
    notes = release_notes(manifest)
    (output / 'RELEASE_NOTES.zh-CN.md').write_text(notes, encoding='utf-8')
    sums = ''.join(f'{digest(p)}  {p.name}\n' for p in sorted(output.iterdir()))
    (output / 'SHA256SUMS').write_text(sums, encoding='utf-8')
    return manifest


def release_notes(manifest):
    links = '\n'.join(f'- {item["name"]}: {item["url"]}，提交 {item["sha"]}' for item in manifest['historical'])
    return f'''# iotools {VERSION}

源码提交：{manifest['source_sha']}。19 个平台包均由此提交重新构建，未把旧 CI 包改名充当发布包。

## 本次修复
- Flutter HTTP 历史默认开启，保留已经保存的关闭选择；响应可能包含敏感信息，可在设置关闭。请求 persist: false 仍不记录，只读保护也不会写历史。
- 请求完成后，正在显示的历史页自动刷新；切换集合不会混入旧列表。
- Windows 图标保留透明背景与原有黑色帽子，四种图标尺寸从生成 PNG 到实际 EXE 资源逐一校验。

## 选择与启动
- TUI：Windows x64、Linux x64/ARM64、macOS Intel/Apple Silicon。完整解压后运行 iotools（Windows 为 iotools.exe）；首次可加 --init。macOS 二进制未做 Developer ID 公证。
- Flutter 桌面：同上五种目标。完整解压并保留 DLL/lib/data，运行 iotools 或 iotools.app。Linux 需要图形会话、GTK 3，基于 Ubuntu 22.04；macOS 13+，仅 ad-hoc 签名、未公证；Windows 无 Authenticode 发行者签名。
- Web：同上五种主机包。运行 iotools-web，浏览器打开终端打印的 127.0.0.1 地址；包含离线界面与本机 Go 网关，单独上传静态网页不提供完整协议功能。
- Android：Android 8/API 26+ 的 ARM64 单架构包和 ARM64/x86_64 双架构通用包，正常入口 Release/AOT，无 instrumentation/integration_test 测试代码。仍用项目原有 debug/test 证书签名，文件名明确标注 test-signed；不是正式发行签名，不能承诺跨构建原地升级，不要作为生产可信签名分发。实际证书 SHA256 见 manifest.json；若签名不同，卸载会删除应用数据，请先自行备份，不要自动卸载。未附测试入口 APK。
- iOS device：arm64 未签名 Release Runner.app ZIP，需自行合法签名；不是可直接安装的 IPA，未做签名真机验收。
- iOS simulator：仅 arm64 Debug 开发包，包含 integration_test 测试通道；不是生产应用。需相应架构 macOS/Xcode。此次包仅编译和静态检查。

## 校验与边界
下载 SHA256SUMS、manifest.json 和所需包。Linux 可运行 sha256sum -c SHA256SUMS（只下载部分包时只核对对应行）；macOS 用 shasum -a 256；Windows 用 Get-FileHash -Algorithm SHA256。清单记录完整源码 SHA、各包哈希、构建运行和静态检查范围。SHA256 用于完整性核对，不替代发行者签名。
本次重新编译、包完整性/依赖/签名状态检查和轻量 smoke 通过后才发布。下列验收覆盖本次应用、锁文件、桥接、版本和编译配置；运行验收提交是此源码提交或应用输入完全相同的直接父提交。最终包由标签提交重新编译，不能把验证二进制视为发布包。Android 保留双架构通用包和单 ARM64 包的编译选项。Flutter SDK 与声明的 Go/NDK/Gradle/Xcode 版本受检；托管 runner、Java 补丁和系统 SDK 可能更新，不声称整个编译环境逐字节相同。每条验收保留真实提交和运行链接；不把父提交的运行改称标签提交重新运行。未扩大 KVM 权限。
{links}
iOS 五项 XCTest 只覆盖普通启动、真实 Go ABI/生命周期、导出边界及系统选择器展示；文件保存/重新导入、全协议界面及签名 iPhone 仍待独立验收。物理串口和真实工业设备也不在此发布保证范围内。
系统若阻止未签名/未公证应用，请使用自己的受信签名构建；不要绕过系统安全警告。完整包内附相应许可证。
'''


def verify_final_ci(api, sha):
    runs = api.request('/actions/workflows/ci.yml/runs?head_sha=' + sha + '&per_page=100')['workflow_runs']
    runs = [run for run in runs if run['head_sha'] == sha and run['head_branch'] == 'main' and run['event'] == 'push']
    require(runs, 'No ordinary main-branch TUI acceptance run exists for release SHA')
    latest = max(runs, key=lambda run: run['id'])
    require(latest['status'] == 'completed' and latest['conclusion'] == 'success', 'Final-SHA main CI is pending or failed; do not publish')
    checks = api.request('/commits/' + sha + '/check-runs?per_page=100')['check_runs']
    require(len(checks) < 100, 'Unexpected check count; paginate/review before publication')
    failures = [row['name'] for row in checks if row['status'] == 'completed' and row['conclusion'] in
                ('failure', 'cancelled', 'timed_out', 'action_required', 'startup_failure', 'stale')]
    require(not failures, 'Final-SHA failed/cancelled checks require review: ' + repr(failures))
    pending = [row for row in checks if row['status'] != 'completed']
    if pending:
        jobs = api.jobs(os.environ['GITHUB_RUN_ID'])
        own = [job for job in jobs if job['name'] == 'publish' and job['status'] == 'in_progress']
        require(len(own) == 1 and own[0].get('check_run_url'), 'Cannot identify this active publisher check')
        own_check_id = int(own[0]['check_run_url'].rsplit('/', 1)[1])
        require(all(row['id'] == own_check_id for row in pending),
                'Other final-SHA checks are still pending: ' + repr([row['name'] for row in pending if row['id'] != own_check_id]))
    return {'run_id': latest['id'], 'url': latest['html_url'], 'head_sha': sha, 'conclusion': 'success'}


def verify_recorded_acceptance(api, sha, manifest):
    accepted, comparison = verify_acceptance(api, sha)
    require(accepted == manifest['historical'] and [comparison] == manifest['source_equivalence'],
            'Runtime acceptance changed after preflight; review before publishing')


def publish(args):
    folder = Path(args.folder)
    manifest = json.loads((folder / 'manifest.json').read_text(encoding="utf-8"))
    require(manifest['publish_requested'] is True, 'This was a dry-run build')
    for field, value in identity(args.sha).items():
        require(manifest.get(field) == value, 'Publish identity mismatch: ' + field)
    require(manifest['run_id'] == os.environ['GITHUB_RUN_ID'] and manifest['run_attempt'] == os.environ['GITHUB_RUN_ATTEMPT'], 'Publish workflow run/attempt mismatch')
    api = GitHub()
    publication_context(api, args.sha, os.environ['GITHUB_EVENT_NAME'], os.environ['GITHUB_REF'], True)
    expected = {filename(key) for key in EXPECTED} | {'manifest.json', 'SHA256SUMS', 'RELEASE_NOTES.zh-CN.md'}
    require({p.name for p in folder.iterdir()} == expected, 'Publication asset inventory mismatch')
    checksums = {}
    for line in (folder / 'SHA256SUMS').read_text(encoding="utf-8").splitlines():
        hash_value, name = line.split('  ', 1)
        require(name in expected - {'SHA256SUMS'} and name not in checksums, 'Invalid checksum manifest')
        require(digest(folder / name) == hash_value, 'Local publication checksum mismatch')
        checksums[name] = hash_value
    require(set(checksums) == expected - {'SHA256SUMS'}, 'Incomplete checksum manifest')
    verify_recorded_acceptance(api, args.sha, manifest)
    verify_final_ci(api, args.sha)
    if resolve_tag(api, VERSION) is None:
        api.request('/git/refs', 'POST', {'ref': 'refs/tags/' + VERSION, 'sha': args.sha})
    require(resolve_tag(api, VERSION) == args.sha, 'Tag moved before release creation')
    # All mutation steps happen only after the complete validated build matrix.
    # A failed upload/remote verification leaves a draft, never a public partial release.
    release = api.request('/releases', 'POST', {'tag_name': VERSION, 'target_commitish': args.sha,
                          'name': 'iotools ' + VERSION, 'draft': True, 'prerelease': False,
                          'body': (folder / 'RELEASE_NOTES.zh-CN.md').read_text(encoding="utf-8")})
    require(release['draft'] is True, 'Server did not create a draft')
    upload_url = release['upload_url'].split('{', 1)[0]
    for path in sorted(folder.iterdir()):
        api.request(upload_url + '?name=' + urllib.parse.quote(path.name), 'POST', path)
    assets = api.request(f'/releases/{release["id"]}/assets?per_page=100')
    require(len(assets) == len(expected) and {a['name'] for a in assets} == expected, 'Remote asset inventory differs')
    for asset in assets:
        require(asset['state'] == 'uploaded' and asset['size'] == (folder / asset['name']).stat().st_size, 'Remote asset incomplete')
        remote = api.request(f'/releases/assets/{asset["id"]}', raw=True)
        require(hashlib.sha256(remote).hexdigest() == digest(folder / asset['name']), 'Remote asset checksum mismatch')
    require(resolve_tag(api, VERSION) == args.sha, 'Tag moved during release upload')
    verify_recorded_acceptance(api, args.sha, manifest)
    verify_final_ci(api, args.sha)
    result = api.request(f'/releases/{release["id"]}', 'PATCH', {'draft': False})
    require(result['draft'] is False and result['tag_name'] == VERSION, 'Release publication not confirmed')
    print(result['html_url'])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    commands = p.add_subparsers(dest='command', required=True)
    q = commands.add_parser('preflight'); q.add_argument('--sha', required=True); q.add_argument('--publish', action='store_true'); q.add_argument('--output', default='release-proof.json')
    q = commands.add_parser('stage'); q.add_argument('--sha', required=True); q.add_argument('--key', choices=sorted(EXPECTED), required=True); q.add_argument('--asset', required=True); q.add_argument('--report', required=True); q.add_argument('--output', default='release-stage')
    q = commands.add_parser('assemble'); q.add_argument('--sha', required=True); q.add_argument('--folder', default='incoming'); q.add_argument('--output', default='release-dist'); q.add_argument('--proof', default='release-proof.json')
    q = commands.add_parser('publish'); q.add_argument('--sha', required=True); q.add_argument('--folder', default='release-dist')
    args = p.parse_args()
    if args.command == 'assemble':
        assemble(args.folder, args.output, json.loads(Path(args.proof).read_text(encoding="utf-8")), args.sha)
    else:
        globals()[args.command](args)


if __name__ == '__main__':
    main()
