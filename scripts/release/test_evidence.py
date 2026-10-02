import json
from pathlib import Path
import tempfile
import unittest
from release_evidence import source_sbom, build_provenance

class EvidenceTests(unittest.TestCase):
    def test_source_inventory_has_scope_and_locked_versions(self):
        report=source_sbom('1'*40)
        self.assertEqual(report['bomFormat'],'CycloneDX')
        self.assertEqual(report['compositions'][0]['aggregate'],'incomplete')
        self.assertGreater(len(report['components']),40)
        self.assertEqual(len({c['bom-ref'] for c in report['components']}),len(report['components']))
        self.assertTrue(any(c['purl'].startswith('pkg:golang/') for c in report['components']))
        self.assertTrue(any(c['purl'].startswith('pkg:pub/') for c in report['components']))
    def test_build_record_is_explicitly_unsigned_and_scoped(self):
        p={'source_sha':'a'*40,'source_inputs_sha256':'b'*64,'tag':'v0.3.2','run_id':'10','run_attempt':'2'}
        out=build_provenance(p,[{'file':'fixture.zip','sha256':'c'*64}],'d'*64)
        self.assertFalse(out['authenticated'])
        self.assertEqual(out['source_sbom_sha256'],'d'*64)
        self.assertEqual(out['subjects'],[{'name':'fixture.zip','sha256':'c'*64}])
        self.assertIn('/runs/10/attempts/2',out['invocation']['url'])
    def test_missing_lock_hash_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/'mobile').mkdir()
            (root/'go.mod').write_text('require (\n example.invalid/a v1.0.0\n)\n')
            (root/'go.sum').write_text('')
            (root/'mobile/pubspec.lock').write_text('packages:\n')
            with self.assertRaisesRegex(RuntimeError,'Missing resolved Go module checksum'):
                source_sbom('a'*40,root)
