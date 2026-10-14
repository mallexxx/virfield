import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import urllib.error
from unittest.mock import patch

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
            body = dict(template='golden', ttl_seconds=3600)
            with self.assertRaises(urllib.error.URLError):
                run.api('leases', body, 'acquire-1')
            body['template'] = 'changed-after-dispatch'
            self.assertEqual(run.requests['acquire-1']['body']['template'], 'golden')
            path = run.destination / 'journal.json'
            self.assertNotIn('private-fixture-token', path.read_text())
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_interrupted_save_preserves_the_last_complete_replay_journal(self):
        with tempfile.TemporaryDirectory() as folder:
            run = module.Acceptance.__new__(module.Acceptance)
            run.destination = Path(folder)
            run.run_id = 'fixture'
            run.requests = {'acquire': dict(path='leases', body=dict(template='golden'))}
            run.accepted = []
            run.results = []
            run.save()
            original = (run.destination / 'journal.json').read_bytes()
            run.accepted.append(dict(id='lease-fixture'))
            with patch.object(module.os, 'replace', side_effect=OSError('interrupted')):
                with self.assertRaises(OSError):
                    run.save()
            self.assertEqual((run.destination / 'journal.json').read_bytes(), original)
            self.assertEqual(list(run.destination.iterdir()), [run.destination / 'journal.json'])

    def test_changed_replay_is_rejected_before_dispatch_or_journal_change(self):
        run = module.Acceptance.__new__(module.Acceptance)
        run.base = 'http://127.0.0.1:7780/api/v1/'
        run.token = 'fixture'
        run.run_id = 'fixture'
        run.requests = {'acquire': dict(path='leases', body=dict(template='first'))}
        with self.assertRaisesRegex(ValueError, 'original path and body'):
            run.api('leases', dict(template='second'), 'acquire')
        self.assertEqual(run.requests['acquire']['body']['template'], 'first')


if __name__ == '__main__':
    unittest.main()
