import hashlib
import os
import subprocess
from pathlib import Path
import tarfile
import tempfile
import unittest

from release import NOTICES_DIRECTORY, write_reproducible_archive


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


    def test_extracted_notice_sources_do_not_enter_go_package_discovery(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            package = root / 'package'
            package.mkdir()
            (package / 'go.mod').write_text('module example.com/package\n\ngo 1.26.9\n')
            (package / 'main.go').write_text('package main\nfunc main() {}\n')
            notices = package / NOTICES_DIRECTORY / 'example.com/dependency@v1.0.0'
            notices.mkdir(parents=True)
            (notices / 'LICENSE.go').write_text('package dependency\n')
            archive = root / 'package.tar.gz'
            write_reproducible_archive(package, archive)
            extracted = root / 'extracted'
            with tarfile.open(archive) as contents:
                # This archive contains only the fixed fixture paths created above.
                contents.extractall(extracted)
            result = subprocess.run(['go', 'list', './...'], cwd=extracted / 'package',
                                    capture_output=True, text=True, check=True,
                                    env=dict(os.environ, GOWORK='off'))
            self.assertEqual(result.stdout.strip(), 'example.com/package')
            self.assertTrue((extracted / 'package' / NOTICES_DIRECTORY /
                             'example.com/dependency@v1.0.0/LICENSE.go').is_file())


if __name__ == '__main__':
    unittest.main()
