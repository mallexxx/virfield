import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import urllib.error

spec = importlib.util.spec_from_file_location('live_acceptance', Path(__file__).with_name('live-acceptance.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AcceptanceSafetyTests(unittest.TestCase):
    def test_missing_cleanup_approval_prevents_effects(self):
        with tempfile.TemporaryDirectory() as folder:
            destination = Path(folder) / 'must-not-exist'
            result = subprocess.run([sys.executable, str(Path(module.__file__)), '--config', '/missing',
                '--binary', '/missing', '--destination', str(destination), 'golden'], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b'Explicit approval', result.stderr)
            self.assertFalse(destination.exists())

    def test_lost_reply_retains_exact_replay_request_without_token(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            token = root / 'token'
            token.write_text('private-fixture-token')
            token.chmod(0o600)
            config = root / 'config.json'
            config.write_text(json.dumps(dict(token_file=str(token), listen='127.0.0.1:7780')))
            run = module.Acceptance(config, Path(sys.executable), root / 'acceptance')
            class LostReply:
                def open(self, request, timeout):
                    journal = json.loads((run.destination / 'journal.json').read_text())
                    self_key = request.get_header('Idempotency-key')
                    self_body = json.loads(request.data)
                    assert self_key == journal['run_id'] + '-acquire-1'
                    assert journal['requests']['acquire-1']['body'] == self_body
                    raise urllib.error.URLError('lost reply')
            run.http = LostReply()
            with self.assertRaises(urllib.error.URLError):
                run.api('leases', dict(template='golden', ttl_seconds=3600), 'acquire-1')
            path = run.destination / 'journal.json'
            self.assertNotIn('private-fixture-token', path.read_text())
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
