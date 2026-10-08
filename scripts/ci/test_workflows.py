"""Local, dependency-free guards for the reviewed CI contract."""
from pathlib import Path
import re
import unittest
import os
import subprocess
import sys
import tempfile
from changed_paths import relevant, PATTERNS
ROOT=Path(__file__).resolve().parents[2]

class WorkflowContracts(unittest.TestCase):
    def test_actions_are_full_immutable_shas(self):
        for path in (ROOT/'.github/workflows').glob('*.yml'):
            for action in re.findall(r'uses:\s*([^\s#]+)',path.read_text()):
                if action.startswith('./'):
                    continue
                self.assertRegex(action,r'^[\w/-]+@[0-9a-f]{40}$',str(path))
    def test_no_new_oidc_or_broad_permissions(self):
        for path in (ROOT/'.github/workflows').glob('*.yml'):
            source=path.read_text()
            self.assertNotIn('id-token:',source)
            self.assertNotIn('write-all',source)
    def test_fast_vulnerability_scan_really_executes(self):
        source=(ROOT/'.github/workflows/fast-pr.yml').read_text()
        self.assertIn('go install golang.org/x/vuln/cmd/govulncheck@v1.8.0',source)
        self.assertIn('govulncheck -show verbose ./...',source)
        self.assertNotIn('continue-on-error',source)
    def test_validation_branch_checks_flutter_before_full_matrix(self):
        source=(ROOT/'.github/workflows/fast-pr.yml').read_text()
        self.assertIn("if: github.event_name == 'push' && github.ref == 'refs/heads/feat/gateway-rejection-validation'",source)
        for command in ('flutter pub get --enforce-lockfile','flutter analyze','flutter test --reporter expanded'):
            self.assertIn(command,source)
        self.assertIn('flutter test test/modbus/modbus_operation_wait_test.dart --reporter expanded',source)
        self.assertIn('6b182d2c7585eba26d4edce0f97630effd256c33',source)
        self.assertNotIn('continue-on-error',source)
    def test_all_flutter_jobs_use_the_reviewed_sdk(self):
        from importlib.util import spec_from_file_location, module_from_spec
        spec = spec_from_file_location('release_config', ROOT/'scripts/release/release_config.py')
        config = module_from_spec(spec)
        spec.loader.exec_module(config)
        for path in (ROOT/'.github/workflows').glob('*.yml'):
            source = path.read_text()
            installs = source.count('https://github.com/flutter/flutter.git')
            if installs:
                self.assertEqual(source.count('--branch 3.44.9 '), installs, str(path))
                self.assertEqual(source.count(config.FLUTTER_REVISION), installs, str(path))
        pubspec = (ROOT/'mobile/pubspec.yaml').read_text()
        self.assertIn('sdk: ">=3.12.0 <4.0.0"', pubspec)
        self.assertIn('flutter: ">=3.44.0"', pubspec)

    def test_full_release_candidate_matrix_remains(self):
        source=(ROOT/'.github/workflows/flutter-platforms.yml').read_text()
        self.assertEqual(source.count("contains(github.event.pull_request.labels.*.name, 'release-candidate')"),4)
        for gate in ('Required real iOS host XCTest acceptance','Exact packaged Windows Release GUI and JVM independence','ubuntu-22.04-arm','macos-15-intel'):
            self.assertIn(gate,source)
    def test_history_paths_cover_engine_api_and_bridge(self):
        for closure in ('internal/engine/**','internal/mobileapi/**','cmd/iotools-native/**'):
            self.assertIn(closure,PATTERNS['history'])
        for name in ('android.yml','history-regression.yml'):
            source=(ROOT/'.github/workflows'/name).read_text()
            self.assertIn('types: [opened, synchronize, reopened, labeled]',source)
            self.assertIn("contains(github.event.pull_request.labels.*.name, 'release-candidate')",source)
            self.assertIn('workflow_dispatch:',source)
    def test_specialty_paths_do_not_skip_shared_runtime(self):
        for suite in ('android','history'):
            self.assertTrue(relevant(suite,['internal/engine/http_history_retention.go']))
            self.assertTrue(relevant(suite,['mobile/lib/features/history/history_page.dart']))
            self.assertFalse(relevant(suite,['docs/releasing.md']))
        self.assertFalse(relevant('android',['mobile/ios/Runner/AppDelegate.swift']))
    def test_rename_out_of_platform_scope_still_selects_it(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            def git(*args):
                return subprocess.check_output(['git','-C',str(root),*args],text=True).strip()
            git('init','-q')
            git('config','user.name','Fixture')
            git('config','user.email','fixture@example.invalid')
            source=root/'mobile/android/app/src/main/AndroidManifest.xml'
            source.parent.mkdir(parents=True)
            source.write_text('<manifest/>')
            git('add','.')
            git('commit','-qm','fixture')
            base=git('rev-parse','HEAD')
            (root/'docs').mkdir()
            source.rename(root/'docs/AndroidManifest.xml')
            git('add','-A')
            git('commit','-qm','rename fixture')
            output=root/'output'
            subprocess.run([sys.executable,str(ROOT/'scripts/ci/changed_paths.py'),'android',base,git('rev-parse','HEAD')],
                           cwd=root,env={**os.environ,'GITHUB_OUTPUT':str(output)},check=True)
            self.assertEqual(output.read_text(),'relevant=true\n')
if __name__=='__main__':unittest.main()
