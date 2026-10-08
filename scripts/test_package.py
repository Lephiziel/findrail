import pathlib
import sys
import tarfile
import tempfile
import unittest
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import package


class PackageSafetyTests(unittest.TestCase):
    def test_manifest_rejects_paths_duplicates_and_mismatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            (root / 'candidate.tar.gz').write_bytes(b'archive')
            digest = package.hashlib.sha256(b'archive').hexdigest()
            for text in (f'{digest}  ../escape.tar.gz\n', f'{digest}  candidate.tar.gz\n{digest}  candidate.tar.gz\n', '0' * 64 + '  candidate.tar.gz\n'):
                (root / 'SHA256SUMS.txt').write_text(text)
                with self.assertRaises(ValueError): package.verify_manifest(root)

    def test_extract_rejects_traversal_and_symlink(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); archive = root / 'bad.tar.gz'
            with tarfile.open(archive, 'w:gz') as tf:
                info = tarfile.TarInfo('pkg/../../escape'); info.size = 1
                import io
                tf.addfile(info, io.BytesIO(b'x'))
            with self.assertRaises(ValueError): package.extract_verified(archive, root / 'out')
            with tarfile.open(archive, 'w:gz') as tf:
                info = tarfile.TarInfo('pkg/link'); info.type = tarfile.SYMTYPE; info.linkname = '../../escape'
                tf.addfile(info)
            with self.assertRaises(ValueError): package.extract_verified(archive, root / 'out')

    def test_zip_rejects_windows_separator_traversal(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = pathlib.Path(temp) / 'bad.zip'
            with zipfile.ZipFile(archive, 'w') as zf:
                zf.writestr('pkg/..\\..\\escape', b'bad')
            with self.assertRaises(ValueError): package.extract_verified(archive, pathlib.Path(temp) / 'out')

    def test_safe_basename(self):
        for name in ('../a', 'a/b', 'C:\\a', ''):
            self.assertFalse(package.safe_basename(name))
        self.assertTrue(package.safe_basename('findrail_0.1.0-alpha.3_linux-amd64.tar.gz'))


if __name__ == '__main__':
    unittest.main()
