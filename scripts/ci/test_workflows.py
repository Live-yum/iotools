"""Local, dependency-free guards for the reviewed CI contract."""
from pathlib import Path
import re
import unittest
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
if __name__=='__main__':unittest.main()
