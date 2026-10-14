"""Exercise the native browser's real configuration trust check without UI."""
import json
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(platform.system() == 'Darwin' and shutil.which('swiftc'), 'native macOS browser')
class BrowserOriginTest(unittest.TestCase):
    def test_split_state_and_private_origin_boundary(self):
        with tempfile.TemporaryDirectory(prefix='virfield-origin-test-') as temporary:
            root = Path(temporary)
            deployment = root / 'deployment'
            deployment.mkdir(mode=0o700)
            state = root / 'state'
            state.mkdir(mode=0o700)
            config = deployment / 'config.json'
            config.write_text(json.dumps(dict(state_dir=str(state), listen='127.0.0.1:7780')))
            config.chmod(0o600)
            harness = root / 'main.swift'
            harness.write_text('''import Foundation
let root = URL(fileURLWithPath: CommandLine.arguments[1])
print(trustedAppleOrigin(deployment: root)?.absoluteString ?? "rejected")
''')
            binary = root / 'probe'
            subprocess.run(['swiftc', '-module-cache-path', str(root / 'cache'),
                str(ROOT / 'apple-browser/TrustedOrigin.swift'), str(harness), '-o', str(binary)], check=True)

            def probe(expected):
                result = subprocess.check_output([str(binary), str(deployment)], text=True).strip()
                self.assertEqual(result, expected)

            probe('http://127.0.0.1:7780')
            for path, private, exposed in ((config, 0o600, 0o644), (state, 0o700, 0o755), (deployment, 0o700, 0o755)):
                path.chmod(exposed)
                probe('rejected')
                path.chmod(private)
            for listen in ('example.com:7780', '127.0.0.1:0', '127.0.0.1:7780?injected=1', 'user@127.0.0.1:7780'):
                config.write_text(json.dumps(dict(state_dir=str(state), listen=listen)))
                probe('rejected')
            config.write_text(json.dumps(dict(state_dir=str(state), listen='127.0.0.1:7780')))
            saved = root / 'saved.json'
            config.rename(saved)
            config.symlink_to(saved)
            probe('rejected')
            config.unlink()
            saved.rename(config)
            state.rmdir()
            state.symlink_to(deployment, target_is_directory=True)
            probe('rejected')


if __name__ == '__main__':
    unittest.main()
