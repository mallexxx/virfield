import importlib.util
import io
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
    def make_run(self, root):
        token = root / 'token'
        token.write_text('private-fixture-token')
        token.chmod(0o600)
        config = root / 'config.json'
        config.write_text(json.dumps(dict(token_file=str(token), listen='127.0.0.1:7780')))
        return module.Acceptance(config, Path(sys.executable), root / 'acceptance')

    def test_tunnel_waits_for_known_rejection_without_leaking_credentials(self):
        with tempfile.TemporaryDirectory() as folder:
            run = self.make_run(Path(folder))
            calls = []
            class Recovering:
                def open(self, request, timeout):
                    calls.append((request.full_url, request.data))
                    if len(calls) == 1:
                        body = dict(error=dict(code='backend_unavailable', retryable=True))
                        raise urllib.error.HTTPError(request.full_url, 503, 'unavailable', {},
                                                     io.BytesIO(json.dumps(body).encode()))
                    return io.BytesIO(b'{"address":"127.0.0.1:4444"}')
            run.http = Recovering()
            with patch.object(module.time, 'sleep'):
                response = run.api('leases/lease-fixture/tunnel', dict(service='desktop'))
            self.assertEqual(response['address'], '127.0.0.1:4444')
            self.assertEqual(calls[0], calls[1])
            self.assertEqual(run.results[0]['code'], 'backend_unavailable')
            self.assertNotIn('private-fixture-token', (run.destination / 'journal.json').read_text())

    def test_vm_mutations_and_nonretryable_tunnels_are_never_auto_replayed(self):
        for path, code, retryable in [('leases', 'backend_unavailable', True),
                                      ('leases/lease-fixture/tunnel', 'tunnel_unavailable', False)]:
            with self.subTest(path=path, code=code), tempfile.TemporaryDirectory() as folder:
                run = self.make_run(Path(folder))
                calls = []
                class Refused:
                    def open(self, request, timeout):
                        calls.append(request)
                        failure = dict(error=dict(code=code, retryable=retryable))
                        raise urllib.error.HTTPError(request.full_url, 503, 'unavailable', {},
                                                     io.BytesIO(json.dumps(failure).encode()))
                run.http = Refused()
                with patch.object(module.time, 'sleep'), self.assertRaises(urllib.error.HTTPError):
                    run.api(path, dict(service='desktop'), 'unchanged-request')
                self.assertEqual(len(calls), 1)
                self.assertEqual(run.requests['unchanged-request']['path'], path)

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
