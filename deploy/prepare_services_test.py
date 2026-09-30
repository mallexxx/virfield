import importlib.util
import json
import os
from pathlib import Path
import plistlib
from types import SimpleNamespace
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('prepare_services', Path(__file__).with_name('prepare-services.py'))
services = importlib.util.module_from_spec(spec)
spec.loader.exec_module(services)


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='virfield install with spaces ')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.account = SimpleNamespace(pw_uid=os.getuid(), pw_name='vm-operator', pw_dir='/srv/VM Owner')
        (self.root / 'bin').mkdir()
        for name in ('virfield', 'virfieldd', 'virfield-mcp', 'virfield-lume', 'lume', 'python', 'tesseract', 'vncdotool'):
            path = self.root / 'bin' / name
            path.write_text('#!/bin/sh\nexit 0\n')
            path.chmod(0o700)
        (self.root / 'token').write_text('fixture-token-never-published')
        (self.root / 'token').chmod(0o600)
        self.config = dict(state_dir=str(self.root), token_file=str(self.root / 'token'),
                           listen='127.0.0.1:18780', lume_url='http://127.0.0.1:18777',
                           image_tools={name: str(self.root / 'bin' / name) for name in ('lume', 'python', 'tesseract')})
        self.config['image_tools']['vnc_bin'] = str(self.root / 'bin')
        self.save()

    def save(self):
        path = self.root / 'config.json'
        path.write_text(json.dumps(self.config))
        path.chmod(0o600)

    def test_alternate_home_paths_spaces_and_ports(self):
        result = services.render(self.root, self.account, 'vm-group')
        manager = plistlib.loads(result['ai.virfield.virfieldd.plist'])
        lume = plistlib.loads(result['ai.virfield.lume.plist'])
        mcp = json.loads(result['mcp.json'])['mcpServers']['virfield']
        self.assertEqual(manager['UserName'], 'vm-operator')
        self.assertEqual(manager['GroupName'], 'vm-group')
        self.assertEqual(manager['EnvironmentVariables']['HOME'], '/srv/VM Owner')
        self.assertEqual(manager['ProgramArguments'][-1], str(self.root / 'config.json'))
        self.assertEqual(lume['ProgramArguments'][-2:], ['-port', '18777'])
        self.assertEqual(mcp['args'][:2], ['-url', 'http://127.0.0.1:18780'])
        self.assertNotIn(b'fixture-token', b''.join(result.values()))
        self.assertFalse((self.root / 'launchd').exists())

    def test_insecure_token_rejected_before_writes(self):
        (self.root / 'token').chmod(0o644)
        with self.assertRaises(ValueError):
            services.render(self.root, self.account, 'staff')
        self.assertFalse((self.root / 'launchd').exists())

    def test_missing_tools_and_remote_lume_rejected(self):
        self.config['lume_url'] = 'http://example.com:7777'
        self.save()
        with self.assertRaises(ValueError):
            services.render(self.root, self.account, 'staff')
        self.config['lume_url'] = 'http://127.0.0.1:7777'
        self.save()
        (self.root / 'bin/lume').unlink()
        with self.assertRaises(ValueError):
            services.render(self.root, self.account, 'staff')


if __name__ == '__main__':
    unittest.main()
