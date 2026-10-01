"""Test packaging integrity without Xcode or a native build."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('ios_verifier', Path(__file__).with_name('verify-ios.py'))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class NoticePackagingTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.root = Path(self.folder.name)
        self.archive = self.root / 'app.zip'
        with zipfile.ZipFile(self.archive, 'w') as archive:
            archive.writestr('Runner.app/Runner', b'tested executable')
            archive.writestr('Runner.app/_CodeSignature/CodeResources', b'original resource signature')
        self.licenses = self.root / 'licenses'
        (self.licenses / 'Go').mkdir(parents=True)
        (self.licenses / 'iotools-LICENSE').write_text('Project license\n')
        data = b'Original Go dependency notice\n'
        (self.licenses / 'Go/dependency.txt').write_bytes(data)
        (self.licenses / 'Go/INDEX.txt').write_text('dependency.txt\n')
        self.manifest = {'Licenses': [{'File': 'dependency.txt', 'SHA256': hashlib.sha256(data).hexdigest()}]}
        self.write_manifest()

    def write_manifest(self):
        (self.licenses / 'Go/MANIFEST.json').write_text(json.dumps(self.manifest))

    def test_adds_exact_notices_without_changing_tested_application(self):
        result = verifier.append_notices(self.archive, self.licenses)
        self.assertEqual(result['go_license_files'], 1)
        with zipfile.ZipFile(self.archive) as archive:
            self.assertEqual(archive.read('Runner.app/Runner'), b'tested executable')
            self.assertEqual(archive.read('Runner.app/_CodeSignature/CodeResources'), b'original resource signature')
            self.assertEqual(archive.read('licenses/Go/dependency.txt'), b'Original Go dependency notice\n')
            self.assertIsNone(archive.testzip())

    def test_corrupt_notice_is_rejected_before_archive_change(self):
        before = self.archive.read_bytes()
        (self.licenses / 'Go/dependency.txt').write_text('changed')
        with self.assertRaisesRegex(RuntimeError, 'hash mismatch'):
            verifier.append_notices(self.archive, self.licenses)
        self.assertEqual(self.archive.read_bytes(), before)

    def test_unsafe_manifest_path_is_rejected(self):
        self.manifest['Licenses'][0]['File'] = '../outside.txt'
        self.write_manifest()
        with self.assertRaisesRegex(RuntimeError, 'Unsafe notice path'):
            verifier.append_notices(self.archive, self.licenses)

    def test_duplicate_notice_addition_is_rejected(self):
        verifier.append_notices(self.archive, self.licenses)
        with self.assertRaisesRegex(RuntimeError, 'duplicate'):
            verifier.append_notices(self.archive, self.licenses)


if __name__ == '__main__':
    unittest.main()
