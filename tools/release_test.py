import hashlib
import os
from pathlib import Path
import tarfile
import tempfile
import unittest

from release import write_reproducible_archive


class ReproducibleArchiveTests(unittest.TestCase):
    def test_mtimes_and_parent_paths_do_not_change_archive(self):
        digests = []
        for stamp in (123456789, 234567890):
            with tempfile.TemporaryDirectory() as temporary:
                package = Path(temporary) / 'virfield-v2.0.1-darwin-arm64'
                (package / 'bin').mkdir(parents=True)
                binary = package / 'bin' / 'virfield'
                binary.write_bytes(b'fixed binary content')
                binary.chmod(0o755)
                app = package / 'bin' / 'VirfieldAppleBrowser.app' / 'Contents'
                (app / 'MacOS').mkdir(parents=True)
                (app / 'Info.plist').write_bytes(b'fixed plist')
                browser = app / 'MacOS' / 'VirfieldAppleBrowser'
                browser.write_bytes(b'fixed browser binary')
                browser.chmod(0o755)
                (package / 'release.json').write_text('{"version":"v2.0.1"}\n')
                for path in (package, package / 'bin', binary, app.parent, app, app / 'MacOS',
                             app / 'Info.plist', browser, package / 'release.json'):
                    os.utime(path, (stamp, stamp))
                archive_path = Path(temporary) / 'release.tar.gz'
                write_reproducible_archive(package, archive_path)
                data = archive_path.read_bytes()
                digests.append(hashlib.sha256(data).hexdigest())
                self.assertEqual(data[4:8], b'\0\0\0\0')  # gzip mtime
                with tarfile.open(archive_path, 'r:gz') as archive:
                    members = archive.getmembers()
                    self.assertEqual([member.name for member in members], sorted(member.name for member in members))
                    self.assertTrue(all(member.uid == 0 and member.gid == 0 and member.mtime == 0 for member in members))
                    self.assertEqual(archive.extractfile('virfield-v2.0.1-darwin-arm64/bin/virfield').read(), b'fixed binary content')
                    self.assertEqual(archive.extractfile('virfield-v2.0.1-darwin-arm64/bin/VirfieldAppleBrowser.app/Contents/MacOS/VirfieldAppleBrowser').read(), b'fixed browser binary')
        self.assertEqual(digests[0], digests[1])


if __name__ == '__main__':
    unittest.main()
