"""Local, dependency-free guards for the reviewed CI contract."""
from pathlib import Path
import re
import unittest
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
        self.assertIn('go install golang.org/x/vuln/cmd/govulncheck@v1.1.4',source)
        self.assertIn('govulncheck -show verbose ./...',source)
        self.assertNotIn('continue-on-error',source)
    def test_full_release_candidate_matrix_remains(self):
        source=(ROOT/'.github/workflows/flutter-platforms.yml').read_text()
        self.assertEqual(source.count("contains(github.event.pull_request.labels.*.name, 'release-candidate')"),4)
        for gate in ('Required real iOS host XCTest acceptance','Exact packaged Windows Release GUI and JVM independence','ubuntu-22.04-arm','macos-15-intel'):
            self.assertIn(gate,source)
    def test_history_paths_cover_engine_api_and_bridge(self):
        source=(ROOT/'.github/workflows/history-regression.yml').read_text()
        for closure in ('internal/engine/**','internal/mobileapi/**','cmd/iotools-native/**','workflow_dispatch:'):
            self.assertIn(closure,source)
if __name__=='__main__':unittest.main()
