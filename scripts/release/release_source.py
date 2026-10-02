"""Exact complete-tree identity and reviewed generated-file validation."""
import hashlib
import json
import re
import shutil
import subprocess
from pathlib import Path
from release_common import ROOT, require, git, digest, write_json
from release_config import VERSION, EXCLUDED_FILES

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
    require(version == VERSION, 'Only the reviewed v0.3.2 release is supported')
    require(git('rev-parse', 'HEAD', root=root) == sha, 'Checkout is not the declared source SHA')
    require(re.search(r'(?m)^version: 0\.3\.2\+5\s*$', (root / 'mobile/pubspec.yaml').read_text(encoding="utf-8")),
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
